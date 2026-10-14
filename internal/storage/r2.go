package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// R2 is the first remote backend (PRD §5.9): Cloudflare R2 through its
// S3-compatible API, by way of minio-go. The key design point is the log on
// an object store that has no append: every event is its own object, named
// so a listing returns them in the order they were written —
//
//	<log>/<20-digit zero-padded unix nanoseconds>-<random>.json
//
// — and written put-if-absent, so no write ever replaces another. There is
// nothing to rewrite, which makes the log append-only by construction.
// Reading a log lists its prefix and fetches each object in key order; a
// hosted instance does that once, at startup, and serves every later read
// from memory (WriteThrough), so the only steady-state cost is one PUT per
// event.
//
// Put-if-absent is a conditional PUT (If-None-Match: *). A store that
// answers it with 501 is one that does not implement the condition; the
// backend then falls back to a HEAD before the PUT, which is not atomic —
// two writers could race — but a hosted instance is the single writer by
// design, and the fallback is reported through ConditionalPut so an
// operator (or the integration test) can see which they got.
type R2 struct {
	client *minio.Client
	bucket string
	prefix string
	// conditional is whether the store honours If-None-Match on PUT: assumed
	// until a 501 says otherwise.
	conditional atomic.Bool
}

// R2Config is what NewR2 needs. Credentials come from the environment —
// never a flag, which shows in process lists — and are read by the caller.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	// Prefix is where in the bucket this instance's keys live; "" is the
	// root.
	Prefix string
	// Endpoint overrides the R2 endpoint derived from AccountID, for a fake
	// in tests or a non-R2 S3 store. host:port, no scheme.
	Endpoint string
	// Insecure speaks plain HTTP to Endpoint, for a local fake only.
	Insecure bool
}

// NewR2 returns a backend. It makes no request: a bad credential surfaces on
// the first call, as an error that does not echo it.
func NewR2(cfg R2Config) (*R2, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("storage: r2: a bucket is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("storage: r2: credentials are required (MARGINALIA_R2_ACCESS_KEY_ID and MARGINALIA_R2_SECRET_ACCESS_KEY)")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		if cfg.AccountID == "" {
			return nil, errors.New("storage: r2: an account id is required (MARGINALIA_R2_ACCOUNT_ID)")
		}
		endpoint = cfg.AccountID + ".r2.cloudflarestorage.com"
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       !cfg.Insecure,
		Region:       "auto",
		BucketLookup: minio.BucketLookupPath,
		Transport:    &http.Transport{MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second, Proxy: http.ProxyFromEnvironment},
	})
	if err != nil {
		return nil, fmt.Errorf("storage: r2: %w", err)
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	r := &R2{client: client, bucket: cfg.Bucket, prefix: prefix}
	r.conditional.Store(true)
	return r, nil
}

// ConditionalPut reports whether the store has honoured If-None-Match so far.
// False means a 501 was seen and the HEAD-then-PUT fallback is in use.
func (r *R2) ConditionalPut() bool { return r.conditional.Load() }

// eventName is how a log's events are named: fixed-width nanoseconds, so
// lexical order is time order, then randomness, so two events in one
// nanosecond cannot share a name.
var eventName = regexp.MustCompile(`^(.*)/(\d{20})-([0-9a-f]{8})\.json$`)

func (r *R2) object(key string) string { return r.prefix + key }

// errCode is the S3 error code of err, or "".
func errCode(err error) string {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code
	}
	return minio.ToErrorResponse(err).Code
}

func isNotImplemented(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NotImplemented" || resp.StatusCode == http.StatusNotImplemented
}

// putIfAbsent writes one object only if nothing is there.
func (r *R2) putIfAbsent(ctx context.Context, object string, data []byte) error {
	if r.conditional.Load() {
		opts := minio.PutObjectOptions{ContentType: "application/octet-stream"}
		opts.SetMatchETagExcept("*")
		_, err := r.client.PutObject(ctx, r.bucket, object, bytes.NewReader(data), int64(len(data)), opts)
		switch {
		case err == nil:
			return nil
		case errCode(err) == "PreconditionFailed":
			return ErrExists
		case isNotImplemented(err):
			// The store does not do conditional writes. Say so once, then
			// use the fallback from here on.
			r.conditional.Store(false)
		default:
			return err
		}
	}
	if _, err := r.client.StatObject(ctx, r.bucket, object, minio.StatObjectOptions{}); err == nil {
		return ErrExists
	} else if code := errCode(err); code != "NoSuchKey" && code != "NotFound" {
		return err
	}
	_, err := r.client.PutObject(ctx, r.bucket, object, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (r *R2) PutBlob(ctx context.Context, key string, data []byte) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	// A blob may not land on a log's key: on an object store the two would
	// coexist — the blob at "k", the events under "k/" — which is exactly
	// the ambiguity a listing then could not resolve. One listing, capped
	// at a single key, is the cost of refusing it.
	if taken, err := r.anyUnder(ctx, key+"/"); err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	} else if taken {
		return fmt.Errorf("storage: put %s: %w", key, ErrExists)
	}
	if err := r.putIfAbsent(ctx, r.object(key), data); err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	return nil
}

// anyUnder reports whether any object has the prefix, with one listing of
// at most one key.
func (r *R2) anyUnder(ctx context.Context, prefix string) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	info, ok := <-r.client.ListObjects(ctx, r.bucket, minio.ListObjectsOptions{Prefix: r.object(prefix), Recursive: true, MaxKeys: 1})
	if !ok {
		return false, nil
	}
	if info.Err != nil {
		return false, info.Err
	}
	return true, nil
}

func (r *R2) GetBlob(ctx context.Context, key string) ([]byte, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	obj, err := r.client.GetObject(ctx, r.bucket, r.object(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		if code := errCode(err); code == "NoSuchKey" || code == "NotFound" {
			return nil, fmt.Errorf("storage: get %s: %w", key, ErrNotFound)
		}
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return data, nil
}

// objects lists the raw object keys under a prefix, in key order, paging as
// the store requires.
func (r *R2) objects(ctx context.Context, prefix string) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var keys []string
	for info := range r.client.ListObjects(ctx, r.bucket, minio.ListObjectsOptions{Prefix: r.object(prefix), Recursive: true}) {
		if info.Err != nil {
			return nil, info.Err
		}
		keys = append(keys, strings.TrimPrefix(info.Key, r.prefix))
	}
	sort.Strings(keys)
	return keys, nil
}

// List returns keys as the interface defines them: a log's events collapse
// into the log's own key, so a caller sees the same layout on every
// backend.
func (r *R2) List(ctx context.Context, prefix string) ([]string, error) {
	if err := CheckPrefix(prefix); err != nil {
		return nil, err
	}
	raw, err := r.objects(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("storage: list %s: %w", prefix, err)
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, k := range raw {
		if m := eventName.FindStringSubmatch(k); m != nil {
			k = m[1]
		}
		if !strings.HasPrefix(k, prefix) || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func (r *R2) AppendLog(ctx context.Context, key string, line []byte) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	if err := checkLine(line); err != nil {
		return err
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	name := fmt.Sprintf("%s/%020d-%s.json", key, time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
	if err := r.putIfAbsent(ctx, r.object(name), line); err != nil {
		return fmt.Errorf("storage: append %s: %w", key, err)
	}
	return nil
}

// events lists one log's event objects, in order.
func (r *R2) events(ctx context.Context, key string) ([]string, error) {
	raw, err := r.objects(ctx, key+"/")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, k := range raw {
		if m := eventName.FindStringSubmatch(k); m != nil && m[1] == key {
			names = append(names, k)
		}
	}
	return names, nil
}

func (r *R2) ReadLog(ctx context.Context, key string) ([][]byte, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	names, err := r.events(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("storage: read %s: %w", key, err)
	}
	var lines [][]byte
	for _, name := range names {
		line, err := r.GetBlob(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("storage: read %s: %w", key, err)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// LogToken is the name of the last event, which moves with every append and
// with nothing else. It costs a listing; a hosted instance never asks — the
// write-through layer answers from memory.
func (r *R2) LogToken(ctx context.Context, key string) (Token, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	names, err := r.events(ctx, key)
	if err != nil {
		return "", fmt.Errorf("storage: token %s: %w", key, err)
	}
	if len(names) == 0 {
		return "", nil
	}
	return Token(names[len(names)-1]), nil
}

// Move copies each object under fromPrefix to toPrefix and deletes the
// original, one at a time, so an interrupted move leaves every object in at
// least one of the two places and a second run finishes it; a copy over an
// object already there is the same bytes, blobs being immutable.
func (r *R2) Move(ctx context.Context, fromPrefix, toPrefix string) error {
	if err := CheckPrefix(fromPrefix); err != nil {
		return err
	}
	if err := CheckPrefix(toPrefix); err != nil {
		return err
	}
	if fromPrefix == "" {
		return fmt.Errorf("%w: refusing to move everything", ErrInvalidKey)
	}
	raw, err := r.objects(ctx, fromPrefix)
	if err != nil {
		return fmt.Errorf("storage: move %s: %w", fromPrefix, err)
	}
	for _, key := range raw {
		dst := toPrefix + strings.TrimPrefix(key, fromPrefix)
		if _, err := r.client.CopyObject(ctx, minio.CopyDestOptions{Bucket: r.bucket, Object: r.object(dst)}, minio.CopySrcOptions{Bucket: r.bucket, Object: r.object(key)}); err != nil {
			return fmt.Errorf("storage: move %s: %w", key, err)
		}
		if err := r.client.RemoveObject(ctx, r.bucket, r.object(key), minio.RemoveObjectOptions{}); err != nil {
			return fmt.Errorf("storage: move %s: %w", key, err)
		}
	}
	return nil
}
