package storage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/s3test"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
)

func fakeR2(t *testing.T, prefix string) (*storage.R2, *s3test.Fake) {
	t.Helper()
	f := s3test.New(t, "marginalia-test")
	r, err := storage.NewR2(storage.R2Config{AccessKeyID: "k", SecretAccessKey: "s", Bucket: f.Bucket(), Prefix: prefix, Endpoint: f.Endpoint(), Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	return r, f
}

// The conformance suite against the in-process fake: no network, no
// credentials, in CI.
func TestR2Conforms(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Store {
		r, _ := fakeR2(t, "")
		return r
	})
	t.Run("under a prefix", func(t *testing.T) {
		storagetest.Run(t, func(t *testing.T) storage.Store {
			r, _ := fakeR2(t, "instances/one")
			return r
		})
	})
	t.Run("through the write-through cache", func(t *testing.T) {
		storagetest.Run(t, func(t *testing.T) storage.Store {
			r, _ := fakeR2(t, "")
			return storage.NewWriteThrough(r)
		})
	})
}

// The log is one object per event, named so a listing returns them in
// order, and a comment costs exactly one PUT.
func TestR2LogIsOneObjectPerEvent(t *testing.T) {
	ctx := context.Background()
	r, f := fakeR2(t, "p")
	for i := 0; i < 3; i++ {
		if err := r.AppendLog(ctx, "reviews/a/log/spec.md", []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.Count("PUT"); got != 3 {
		t.Errorf("three appends cost %d PUTs, want 3", got)
	}
	keys := f.Keys()
	if len(keys) != 3 {
		t.Fatalf("objects: %v", keys)
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, "p/reviews/a/log/spec.md/") || !strings.HasSuffix(k, ".json") {
			t.Errorf("event object %q is not named under its log", k)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(k, "p/reviews/a/log/spec.md/"), ".json")
		if len(name) != 20+1+8 {
			t.Errorf("event name %q is not <20 digits>-<8 hex>", name)
		}
	}
	lines, err := r.ReadLog(ctx, "reviews/a/log/spec.md")
	if err != nil || len(lines) != 3 || string(lines[2]) != `{"n":2}` {
		t.Fatalf("read back: %q, %v", lines, err)
	}
	// Export sees the familiar sequence: the log key, not the objects.
	got, _ := r.List(ctx, "reviews/a/")
	if len(got) != 1 || got[0] != "reviews/a/log/spec.md" {
		t.Errorf("List collapses events into the log key: got %v", got)
	}
	if r.ConditionalPut() != true {
		t.Error("the fake honours conditional PUT; the backend thinks otherwise")
	}
}

// A store without conditional writes answers 501; the backend falls back to
// HEAD-then-PUT, keeps the put-if-absent contract, and reports it.
func TestR2FallsBackWhenConditionalPutIsNotImplemented(t *testing.T) {
	ctx := context.Background()
	r, f := fakeR2(t, "")
	f.NoConditionalPut = true
	if err := r.PutBlob(ctx, "reviews/a/meta/1.json", []byte("1")); err != nil {
		t.Fatalf("first put under the fallback: %v", err)
	}
	if r.ConditionalPut() {
		t.Error("the fallback is not reported")
	}
	if err := r.PutBlob(ctx, "reviews/a/meta/1.json", []byte("2")); !errors.Is(err, storage.ErrExists) {
		t.Fatalf("second put under the fallback: %v, want ErrExists", err)
	}
	got, _ := r.GetBlob(ctx, "reviews/a/meta/1.json")
	if string(got) != "1" {
		t.Errorf("the fallback overwrote: %q", got)
	}
	// And the suite still passes on such a store.
	storagetest.Run(t, func(t *testing.T) storage.Store {
		r, f := fakeR2(t, "")
		f.NoConditionalPut = true
		return r
	})
}

// A listing pages. The fake returns at most max-keys per page, and a client
// that did not follow the continuation token would see 1000 of 1200.
func TestR2ListsPastOnePage(t *testing.T) {
	ctx := context.Background()
	r, _ := fakeR2(t, "")
	for i := 0; i < 1200; i++ {
		if err := r.PutBlob(ctx, fmt.Sprintf("reviews/a/rev/1/d%04d.md", i), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := r.List(ctx, "reviews/a/rev/1/")
	if err != nil || len(keys) != 1200 {
		t.Fatalf("listed %d keys, %v", len(keys), err)
	}
}

func TestR2ConfigRefusesWhatItCannotUse(t *testing.T) {
	const secret = "SECRET-7f3a9c1e"
	for name, cfg := range map[string]storage.R2Config{
		"no bucket":      {AccessKeyID: "k", SecretAccessKey: secret, AccountID: "a"},
		"no credentials": {Bucket: "b", AccountID: "a"},
		"no account":     {Bucket: "b", AccessKeyID: "k", SecretAccessKey: secret},
	} {
		if _, err := storage.NewR2(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the error echoes the secret", name)
		}
	}
	// A wrong credential surfaces on the first call, and the error does not
	// carry it either.
	f := s3test.New(t, "b")
	r, err := storage.NewR2(storage.R2Config{AccessKeyID: "k", SecretAccessKey: secret, Bucket: "nope", Endpoint: f.Endpoint(), Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetBlob(context.Background(), "x"); err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("a failing call: %v", err)
	}
}

// The integration test: the same suite against a real bucket, gated on the
// MARGINALIA_R2_* variables and skipped without them. It also checks that R2
// honours the conditional PUT the backend relies on — if it did not,
// ConditionalPut would read false after the first refused overwrite and this
// test would say so.
func TestR2Integration(t *testing.T) {
	account, key, secret, bucket := os.Getenv("MARGINALIA_R2_ACCOUNT_ID"), os.Getenv("MARGINALIA_R2_ACCESS_KEY_ID"), os.Getenv("MARGINALIA_R2_SECRET_ACCESS_KEY"), os.Getenv("MARGINALIA_R2_BUCKET")
	if account == "" || key == "" || secret == "" || bucket == "" {
		t.Skip("set MARGINALIA_R2_ACCOUNT_ID, MARGINALIA_R2_ACCESS_KEY_ID, MARGINALIA_R2_SECRET_ACCESS_KEY and MARGINALIA_R2_BUCKET to run against a real bucket")
	}
	prefix := fmt.Sprintf("storagetest/%d", time.Now().UnixNano())
	open := func(t *testing.T) storage.Store {
		r, err := storage.NewR2(storage.R2Config{AccountID: account, AccessKeyID: key, SecretAccessKey: secret, Bucket: bucket, Prefix: prefix + "/" + t.Name()})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	storagetest.Run(t, open)
	t.Run("conditional put is honoured", func(t *testing.T) {
		r := open(t).(*storage.R2)
		ctx := context.Background()
		if err := r.PutBlob(ctx, "x/1", []byte("1")); err != nil {
			t.Fatal(err)
		}
		if err := r.PutBlob(ctx, "x/1", []byte("2")); !errors.Is(err, storage.ErrExists) {
			t.Fatalf("overwrite: %v", err)
		}
		if !r.ConditionalPut() {
			t.Fatal("R2 answered the conditional PUT with 501: the HEAD-then-PUT fallback is in use, and the single-writer rule is what keeps put-if-absent true")
		}
	})
}
