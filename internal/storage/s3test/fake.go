// Package s3test is an in-process S3-compatible object store for tests: the
// handful of operations the R2 backend uses, with the semantics that matter
// to it — a conditional PUT that refuses an existing key, a paginated
// listing, a copy and a delete — and a request counter, so a test can say
// what a code path cost. No network, no credentials, no signature checks.
package s3test

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Fake is one bucket behind an httptest server.
type Fake struct {
	t      *testing.T
	bucket string
	srv    *httptest.Server
	mu     sync.Mutex
	obj    map[string][]byte
	counts map[string]int
	// NoConditionalPut makes the fake answer a conditional PUT with 501, as
	// a store that does not implement If-None-Match would, so the backend's
	// fallback can be exercised.
	NoConditionalPut bool
}

// New starts a fake holding one bucket.
func New(t *testing.T, bucket string) *Fake {
	t.Helper()
	f := &Fake{t: t, bucket: bucket, obj: map[string][]byte{}, counts: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// Endpoint is host:port, as a client wants it.
func (f *Fake) Endpoint() string { return strings.TrimPrefix(f.srv.URL, "http://") }

// Bucket is the one bucket's name.
func (f *Fake) Bucket() string { return f.bucket }

// Count is how many requests of one method (PUT, GET, HEAD, DELETE, LIST,
// COPY) have arrived since the last Reset.
func (f *Fake) Count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[op]
}

// Total is every request since the last Reset.
func (f *Fake) Total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.counts {
		n += c
	}
	return n
}

// Reset zeroes the counters.
func (f *Fake) Reset() {
	f.mu.Lock()
	f.counts = map[string]int{}
	f.mu.Unlock()
}

// Keys lists every object key, sorted.
func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.obj))
	for k := range f.obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (f *Fake) count(op string) {
	f.mu.Lock()
	f.counts[op]++
	f.mu.Unlock()
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket != f.bucket {
		s3Error(w, http.StatusNotFound, "NoSuchBucket", "no such bucket")
		return
	}
	switch {
	case key == "" && r.Method == http.MethodGet && r.URL.Query().Has("location"):
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">auto</LocationConstraint>`)
	case key == "" && r.Method == http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case key == "" && r.Method == http.MethodGet:
		f.count("LIST")
		f.list(w, r.URL.Query())
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		f.count("COPY")
		f.copy(w, r, key)
	case r.Method == http.MethodPut:
		f.count("PUT")
		f.put(w, r, key)
	case r.Method == http.MethodGet:
		f.count("GET")
		f.get(w, key, true)
	case r.Method == http.MethodHead:
		f.count("HEAD")
		f.get(w, key, false)
	case r.Method == http.MethodDelete:
		f.count("DELETE")
		f.mu.Lock()
		delete(f.obj, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		s3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (f *Fake) put(w http.ResponseWriter, r *http.Request, key string) {
	body, err := readBody(r)
	if err != nil {
		s3Error(w, http.StatusBadRequest, "IncompleteBody", err.Error())
		return
	}
	conditional := r.Header.Get("If-None-Match") == "*"
	if conditional && f.NoConditionalPut {
		s3Error(w, http.StatusNotImplemented, "NotImplemented", "conditional writes are not supported")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.obj[key]; exists && conditional {
		s3Error(w, http.StatusPreconditionFailed, "PreconditionFailed", "the object already exists")
		return
	}
	f.obj[key] = body
	w.Header().Set("ETag", etag(body))
	w.WriteHeader(http.StatusOK)
}

func (f *Fake) get(w http.ResponseWriter, key string, body bool) {
	f.mu.Lock()
	data, ok := f.obj[key]
	f.mu.Unlock()
	if !ok {
		if body {
			s3Error(w, http.StatusNotFound, "NoSuchKey", "no such key")
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("ETag", etag(data))
	w.Header().Set("Last-Modified", "Tue, 14 Oct 2026 00:00:00 GMT")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	if body {
		_, _ = w.Write(data)
	}
}

func (f *Fake) copy(w http.ResponseWriter, r *http.Request, key string) {
	src, _ := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
	src = strings.TrimPrefix(src, "/")
	_, srcKey, _ := strings.Cut(src, "/")
	f.mu.Lock()
	data, ok := f.obj[srcKey]
	if ok {
		f.obj[key] = bytes.Clone(data)
	}
	f.mu.Unlock()
	if !ok {
		s3Error(w, http.StatusNotFound, "NoSuchKey", "no such source")
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><CopyObjectResult><LastModified>2026-10-14T00:00:00.000Z</LastModified><ETag>%s</ETag></CopyObjectResult>`, etag(data))
}

type listResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Xmlns                 string   `xml:"xmlns,attr"`
	Name                  string   `xml:"Name"`
	Prefix                string   `xml:"Prefix"`
	KeyCount              int      `xml:"KeyCount"`
	MaxKeys               int      `xml:"MaxKeys"`
	IsTruncated           bool     `xml:"IsTruncated"`
	ContinuationToken     string   `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string   `xml:"NextContinuationToken,omitempty"`
	Contents              []listEntry
}

type listEntry struct {
	XMLName      xml.Name `xml:"Contents"`
	Key          string   `xml:"Key"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
	Size         int      `xml:"Size"`
	StorageClass string   `xml:"StorageClass"`
}

// list is ListObjectsV2, paginated with a continuation token so a client
// that forgets to page is caught by the tests.
func (f *Fake) list(w http.ResponseWriter, q url.Values) {
	prefix := q.Get("prefix")
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxKeys = n
		}
	}
	after := q.Get("continuation-token")
	if after == "" {
		after = q.Get("start-after")
	}
	f.mu.Lock()
	keys := make([]string, 0, len(f.obj))
	for k := range f.obj {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	res := listResult{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Name: f.bucket, Prefix: prefix, MaxKeys: maxKeys, ContinuationToken: q.Get("continuation-token")}
	for _, k := range keys {
		if len(res.Contents) == maxKeys {
			res.IsTruncated = true
			res.NextContinuationToken = res.Contents[len(res.Contents)-1].Key
			break
		}
		res.Contents = append(res.Contents, listEntry{Key: k, LastModified: "2026-10-14T00:00:00.000Z", ETag: etag(f.obj[k]), Size: len(f.obj[k]), StorageClass: "STANDARD"})
	}
	f.mu.Unlock()
	res.KeyCount = len(res.Contents)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(res)
}

// readBody returns the object bytes, decoding the aws-chunked framing a V4
// streaming signature wraps them in over plain HTTP.
func readBody(r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") && !strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
		return raw, nil
	}
	var out bytes.Buffer
	br := bufio.NewReader(bytes.NewReader(raw))
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("aws-chunked: %w", err)
		}
		sizeHex, _, _ := strings.Cut(strings.TrimSpace(line), ";")
		size, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("aws-chunked: bad chunk size %q", sizeHex)
		}
		if size == 0 {
			return out.Bytes(), nil
		}
		if _, err := io.CopyN(&out, br, size); err != nil {
			return nil, fmt.Errorf("aws-chunked: %w", err)
		}
		if _, err := br.ReadString('\n'); err != nil { // the CRLF after the chunk
			return nil, fmt.Errorf("aws-chunked: %w", err)
		}
	}
}

func etag(data []byte) string {
	return fmt.Sprintf("%q", fmt.Sprintf("%x", len(data))+"-"+strconv.Itoa(int(sum(data))))
}

func sum(data []byte) uint32 {
	var s uint32 = 2166136261
	for _, b := range data {
		s = (s ^ uint32(b)) * 16777619
	}
	return s
}

func s3Error(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, msg) //nolint:gosec // a test fake answering an S3 client, not a browser
}
