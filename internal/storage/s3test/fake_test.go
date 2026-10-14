package s3test

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
)

func request(t *testing.T, f *Fake, method, path string, body []byte, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+f.Endpoint()+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res, string(out)
}

// The fake's own contract, so the backend tests built on it stand on
// something checked: a conditional PUT refuses an existing key with 412, a
// plain PUT overwrites, GET and HEAD agree, DELETE forgets, and the counters
// count.
func TestFakeConditionalPut(t *testing.T) {
	f := New(t, "b")
	if res, _ := request(t, f, "PUT", "/b/k", []byte("one"), map[string]string{"If-None-Match": "*"}); res.StatusCode != 200 {
		t.Fatalf("first conditional PUT: %d", res.StatusCode)
	}
	res, body := request(t, f, "PUT", "/b/k", []byte("two"), map[string]string{"If-None-Match": "*"})
	if res.StatusCode != http.StatusPreconditionFailed || !strings.Contains(body, "PreconditionFailed") {
		t.Fatalf("second conditional PUT: %d %s", res.StatusCode, body)
	}
	if _, body := request(t, f, "GET", "/b/k", nil, nil); body != "one" {
		t.Errorf("GET after a refused PUT = %q", body)
	}
	if res, _ := request(t, f, "PUT", "/b/k", []byte("three"), nil); res.StatusCode != 200 {
		t.Fatalf("plain PUT: %d", res.StatusCode)
	}
	if _, body := request(t, f, "GET", "/b/k", nil, nil); body != "three" {
		t.Errorf("GET after a plain PUT = %q", body)
	}
	if res, _ := request(t, f, "HEAD", "/b/k", nil, nil); res.StatusCode != 200 || res.Header.Get("Content-Length") != "5" {
		t.Errorf("HEAD: %d %s", res.StatusCode, res.Header.Get("Content-Length"))
	}
	if res, _ := request(t, f, "DELETE", "/b/k", nil, nil); res.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE: %d", res.StatusCode)
	}
	if res, body := request(t, f, "GET", "/b/k", nil, nil); res.StatusCode != 404 || !strings.Contains(body, "NoSuchKey") {
		t.Errorf("GET after DELETE: %d %s", res.StatusCode, body)
	}
	if res, _ := request(t, f, "HEAD", "/b/k", nil, nil); res.StatusCode != 404 {
		t.Errorf("HEAD after DELETE: %d", res.StatusCode)
	}
	if f.Count("PUT") != 3 || f.Count("GET") != 3 || f.Count("HEAD") != 2 || f.Count("DELETE") != 1 || f.Total() != 9 {
		t.Errorf("counts: PUT %d GET %d HEAD %d DELETE %d total %d", f.Count("PUT"), f.Count("GET"), f.Count("HEAD"), f.Count("DELETE"), f.Total())
	}
	f.Reset()
	if f.Total() != 0 {
		t.Error("Reset did not zero the counters")
	}
	f.NoConditionalPut = true
	if res, body := request(t, f, "PUT", "/b/k2", []byte("x"), map[string]string{"If-None-Match": "*"}); res.StatusCode != http.StatusNotImplemented || !strings.Contains(body, "NotImplemented") {
		t.Errorf("conditional PUT with NoConditionalPut: %d %s", res.StatusCode, body)
	}
	if res, _ := request(t, f, "PUT", "/b/k2", []byte("x"), nil); res.StatusCode != 200 {
		t.Errorf("plain PUT with NoConditionalPut: %d", res.StatusCode)
	}
}

type listing struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Keys                  []string `xml:"Contents>Key"`
}

func TestFakeListsByPrefixAndPages(t *testing.T) {
	f := New(t, "b")
	for _, k := range []string{"a/1", "a/2", "a/3", "b/1"} {
		request(t, f, "PUT", "/b/"+k, []byte(k), nil)
	}
	res, body := request(t, f, "GET", "/b?list-type=2&prefix=a/&max-keys=2", nil, nil)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	var page listing
	if err := xml.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Keys) != 2 || !page.IsTruncated || page.NextContinuationToken != "a/2" {
		t.Fatalf("first page: %+v", page)
	}
	_, body = request(t, f, "GET", "/b?list-type=2&prefix=a/&max-keys=2&continuation-token="+page.NextContinuationToken, nil, nil)
	var next listing
	_ = xml.Unmarshal([]byte(body), &next)
	if len(next.Keys) != 1 || next.Keys[0] != "a/3" || next.IsTruncated {
		t.Fatalf("second page: %+v", next)
	}
	if got := f.Keys(); strings.Join(got, ",") != "a/1,a/2,a/3,b/1" {
		t.Errorf("Keys = %v", got)
	}
	if f.Count("LIST") != 2 {
		t.Errorf("LIST count = %d", f.Count("LIST"))
	}
	// Bucket-level probes a client makes.
	if res, body := request(t, f, "GET", "/b?location=", nil, nil); res.StatusCode != 200 || !strings.Contains(body, "LocationConstraint") {
		t.Errorf("location: %d %s", res.StatusCode, body)
	}
	if res, _ := request(t, f, "HEAD", "/b", nil, nil); res.StatusCode != 200 {
		t.Errorf("HEAD bucket: %d", res.StatusCode)
	}
	if res, body := request(t, f, "GET", "/other/k", nil, nil); res.StatusCode != 404 || !strings.Contains(body, "NoSuchBucket") {
		t.Errorf("another bucket: %d %s", res.StatusCode, body)
	}
	if res, _ := request(t, f, "PATCH", "/b/k", nil, nil); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("PATCH: %d", res.StatusCode)
	}
}

func TestFakeCopies(t *testing.T) {
	f := New(t, "b")
	request(t, f, "PUT", "/b/src", []byte("payload"), nil)
	res, body := request(t, f, "PUT", "/b/dst", nil, map[string]string{"X-Amz-Copy-Source": "/b/src"})
	if res.StatusCode != 200 || !strings.Contains(body, "CopyObjectResult") {
		t.Fatalf("copy: %d %s", res.StatusCode, body)
	}
	if _, body := request(t, f, "GET", "/b/dst", nil, nil); body != "payload" {
		t.Errorf("copied bytes = %q", body)
	}
	if res, _ := request(t, f, "PUT", "/b/dst2", nil, map[string]string{"X-Amz-Copy-Source": "/b/missing"}); res.StatusCode != 404 {
		t.Errorf("copy of a missing source: %d", res.StatusCode)
	}
	if f.Count("COPY") != 2 {
		t.Errorf("COPY count = %d", f.Count("COPY"))
	}
}

// A V4 streaming signature frames the body in aws-chunked; the fake stores
// the bytes, not the framing.
func TestFakeDecodesAWSChunked(t *testing.T) {
	f := New(t, "b")
	framed := "5;chunk-signature=abc\r\nhello\r\n6;chunk-signature=def\r\n world\r\n0;chunk-signature=ghi\r\n\r\n"
	res, _ := request(t, f, "PUT", "/b/k", []byte(framed), map[string]string{"X-Amz-Content-Sha256": "STREAMING-AWS4-HMAC-SHA256-PAYLOAD", "Content-Encoding": "aws-chunked"})
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if _, body := request(t, f, "GET", "/b/k", nil, nil); body != "hello world" {
		t.Errorf("stored %q", body)
	}
	if res, _ := request(t, f, "PUT", "/b/bad", []byte("zz;chunk-signature=x\r\n"), map[string]string{"X-Amz-Content-Sha256": "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed frame: %d", res.StatusCode)
	}
}
