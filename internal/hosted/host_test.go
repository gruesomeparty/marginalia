package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/s3test"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
)

const (
	specDoc = "# Spec\n\nCapped at 500 for now.\n\n## Retry\n\nThree attempts, no backoff.\n"
	planDoc = "# Plan\n\nShip it.\n"
)

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Logf("host: %s", p); return len(p), nil }

// backends is every store a host has to work on identically. The in-memory
// one is what proves the host has no hidden os dependency.
func backends(t *testing.T) map[string]storage.Store {
	t.Helper()
	return map[string]storage.Store{
		"memory": storage.NewMemory(),
		"file":   storage.NewDisk(t.TempDir()),
		"r2":     fakeR2(t),
	}
}

// fakeR2 is the R2 backend over an in-process S3 fake.
func fakeR2(t *testing.T) *storage.R2 {
	t.Helper()
	r, _ := fakeR2WithCounts(t)
	return r
}

func fakeR2WithCounts(t *testing.T) (*storage.R2, *s3test.Fake) {
	t.Helper()
	f := s3test.New(t, "marginalia")
	r, err := storage.NewR2(storage.R2Config{AccessKeyID: "k", SecretAccessKey: "s", Bucket: f.Bucket(), Prefix: "inst", Endpoint: f.Endpoint(), Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	return r, f
}

func open(t *testing.T, store storage.Store) (*Host, *httptest.Server) {
	t.Helper()
	h, err := Open(context.Background(), Options{Store: store, PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com", Log: testWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return h, ts
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func post(t *testing.T, url string, e feedback.Event) int {
	t.Helper()
	body, _ := json.Marshal(e)
	res, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

func publish(t *testing.T, h *Host, title string, docs ...Document) *Review {
	t.Helper()
	r, err := h.Publish(context.Background(), Publish{Title: title, Agent: "agent", Docs: docs})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The acceptance criterion of #95, on every backend: two reviews, each at
// its own prefix; a comment lands in one log only; a restart on the same
// store brings both back with the comment; unknown ids are 404.
func TestHostServesReviewsUnderTheirOwnPrefixAndSurvivesRestart(t *testing.T) {
	for name, store := range backends(t) {
		t.Run(name, func(t *testing.T) {
			h, ts := open(t, store)
			a := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
			b := publish(t, h, "plan", Document{Name: "plan.md", Content: []byte(planDoc)})
			if a.Meta.ID == b.Meta.ID || !ValidID(a.Meta.ID) {
				t.Fatalf("ids: %q %q", a.Meta.ID, b.Meta.ID)
			}
			if h.URL(a.Meta.ID) != "https://review.example/r/"+a.Meta.ID+"/" {
				t.Errorf("URL = %s: must be built from the public URL", h.URL(a.Meta.ID))
			}

			for _, r := range []*Review{a, b} {
				code, body := get(t, ts.URL+"/r/"+r.Meta.ID+"/")
				if code != 200 || !strings.Contains(body, "window.__MARGINALIA__") {
					t.Fatalf("page for %s: %d", r.Meta.ID, code)
				}
				if !strings.Contains(body, `"base":"/r/`+r.Meta.ID+`"`) {
					t.Errorf("the page does not know its mount: %s", r.Meta.ID)
				}
				if !strings.Contains(body, `"inbox":"/"`) || !strings.Contains(body, `id="back" href="/"`) {
					t.Errorf("the page has no way back to the inbox: %s", r.Meta.ID)
				}
				code, body = get(t, ts.URL+"/r/"+r.Meta.ID+"/api/doc")
				if code != 200 {
					t.Fatalf("/api/doc for %s: %d", r.Meta.ID, code)
				}
				var doc struct {
					Doc struct {
						Path string `json:"path"`
					} `json:"doc"`
				}
				if err := json.Unmarshal([]byte(body), &doc); err != nil {
					t.Fatal(err)
				}
				if doc.Doc.Path != r.Meta.Docs[0] {
					t.Errorf("/api/doc for %s describes %q", r.Meta.ID, doc.Doc.Path)
				}
			}
			// A document page under the prefix works too, with the prefix
			// in every link it renders.
			if code, _ := get(t, ts.URL+"/r/"+a.Meta.ID+"/d/spec.md"); code != 200 {
				t.Errorf("/d/spec.md under the prefix: %d", code)
			}
			// No slash: redirected, since the page's URLs are relative to
			// its mount.
			res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(ts.URL + "/r/" + a.Meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusMovedPermanently || !strings.HasSuffix(res.Header.Get("Location"), "/r/"+a.Meta.ID+"/") {
				t.Errorf("/r/<id> without a slash: %d → %q", res.StatusCode, res.Header.Get("Location"))
			}

			if code := post(t, ts.URL+"/r/"+a.Meta.ID+"/api/feedback", feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "why 500?", Author: "berkay"}); code != http.StatusCreated {
				t.Fatalf("POST comment: %d", code)
			}
			_, aLog := get(t, ts.URL+"/r/"+a.Meta.ID+"/api/feedback")
			_, bLog := get(t, ts.URL+"/r/"+b.Meta.ID+"/api/feedback")
			if !strings.Contains(aLog, "why 500?") {
				t.Errorf("the comment did not land in its review: %s", aLog)
			}
			if strings.Contains(bLog, "why 500?") {
				t.Errorf("the comment leaked into the other review: %s", bLog)
			}
			lines, err := store.ReadLog(context.Background(), "reviews/"+a.Meta.ID+"/log/spec.md")
			if err != nil || len(lines) != 1 {
				t.Fatalf("the log in the store: %v, %v", lines, err)
			}
			if other, _ := store.ReadLog(context.Background(), "reviews/"+b.Meta.ID+"/log/plan.md"); len(other) != 0 {
				t.Errorf("the other review's log is not empty: %q", other)
			}

			for _, bad := range []string{"/r/nope/", "/r/" + strings.Repeat("A", 22) + "/", "/r/" + a.Meta.ID + "x/", "/r/../x/"} {
				if code, _ := get(t, ts.URL+bad); code != http.StatusNotFound {
					t.Errorf("GET %s = %d, want 404", bad, code)
				}
			}

			// Restart: a new host on the same store, nothing else carried over.
			ts.Close()
			h2, ts2 := open(t, store)
			if got := len(h2.Reviews()); got != 2 {
				t.Fatalf("after restart: %d reviews, want 2", got)
			}
			code, body := get(t, ts2.URL+"/r/"+a.Meta.ID+"/api/feedback")
			if code != 200 || !strings.Contains(body, "why 500?") {
				t.Errorf("the comment did not survive the restart: %d %s", code, body)
			}
			code, body = get(t, ts2.URL+"/r/"+b.Meta.ID+"/")
			if code != 200 || !strings.Contains(body, "Ship it.") {
				t.Errorf("the second review did not survive the restart: %d", code)
			}
			if h2.Review(b.Meta.ID).Meta.Title != "plan" {
				t.Errorf("metadata did not survive: %+v", h2.Review(b.Meta.ID).Meta)
			}
		})
	}
}

// After startup, serving pages and reading feedback costs the store nothing:
// state is in memory and written through. A comment is exactly one write.
func TestHostReadsTheStoreOnceAtStartup(t *testing.T) {
	counting := storagetest.NewCounting(storage.NewMemory())
	h, _ := open(t, counting)
	a := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	// A fresh process on the same store: the listing and the reads happen
	// here, once.
	h2, ts := open(t, counting)
	counting.Reset()
	for i := 0; i < 5; i++ {
		if code, _ := get(t, ts.URL+"/r/"+a.Meta.ID+"/"); code != 200 {
			t.Fatal(code)
		}
		if code, _ := get(t, ts.URL+"/r/"+a.Meta.ID+"/api/resolution"); code != 200 {
			t.Fatal(code)
		}
		for _, p := range []string{"/", "/api/reviews", "/stats", "/api/stats"} {
			if code, _ := get(t, ts.URL+p); code != 200 {
				t.Fatalf("%s: %d", p, code)
			}
		}
	}
	if got := counting.Reads(); got != 0 {
		t.Errorf("rendering pages cost %d store reads after startup, want 0", got)
	}
	if code := post(t, ts.URL+"/r/"+a.Meta.ID+"/api/feedback", feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "n", Author: "b"}); code != http.StatusCreated {
		t.Fatal(code)
	}
	if got := counting.Writes(); got != 1 {
		t.Errorf("a comment cost %d store writes, want exactly 1", got)
	}
	if got := counting.Reads(); got != 0 {
		t.Errorf("a comment cost %d store reads, want 0", got)
	}
	_ = h2
}

func TestPublishRefusesBeforeWritingAnything(t *testing.T) {
	counting := storagetest.NewCounting(storage.NewMemory())
	h, _ := open(t, counting)
	counting.Reset()
	cases := map[string]Publish{
		"no documents":    {},
		"empty document":  {Docs: []Document{{Name: "spec.md"}}},
		"unsupported":     {Docs: []Document{{Name: "spec.rst", Content: []byte("x")}}},
		"traversal":       {Docs: []Document{{Name: "../x.md", Content: []byte("x")}}},
		"hidden":          {Docs: []Document{{Name: ".env", Content: []byte("x")}}},
		"nested":          {Docs: []Document{{Name: "a/b.md", Content: []byte("x")}}},
		"duplicate":       {Docs: []Document{{Name: "a.md", Content: []byte("x")}, {Name: "a.md", Content: []byte("y")}}},
		"bad config":      {Docs: []Document{{Name: "a.md", Content: []byte("x")}}, Config: "nonsense: 1\n"},
		"unknown preset":  {Docs: []Document{{Name: "a.md", Content: []byte("x")}}, Review: "nope"},
		"unknown theme":   {Docs: []Document{{Name: "a.md", Content: []byte("x")}}, Theme: "nope"},
		"too large":       {Docs: []Document{{Name: "a.md", Content: bytes.Repeat([]byte("x"), int(DefaultLimits().DocumentBytes)+1)}}},
		"malformed input": {Docs: []Document{{Name: "a.json", Content: []byte("{")}}},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := h.Publish(context.Background(), p); err == nil {
				t.Fatal("published")
			}
			if got := counting.Writes(); got != 0 {
				t.Fatalf("a refused publish wrote %d time(s)", got)
			}
			if got := len(h.Reviews()); got != 0 {
				t.Fatalf("a refused publish is being served")
			}
		})
	}
	_, err := h.Publish(context.Background(), cases["unsupported"])
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Errorf("an unsupported format must advertise the feedback loop: %v", err)
	}
}

func TestOpenStoreURIs(t *testing.T) {
	for _, tc := range []struct {
		uri string
		ok  bool
	}{
		{"file:///tmp/x", true},
		{"file://./rel", true},
		{"file://rel", true},
		{"file://", false},
		{"/tmp/x", false},
		{"s3://bucket", false},
	} {
		_, where, err := OpenStore(tc.uri)
		if tc.ok && (err != nil || !strings.HasPrefix(where, "file:///")) {
			t.Errorf("OpenStore(%q) = %q, %v", tc.uri, where, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("OpenStore(%q) accepted", tc.uri)
		}
	}
	_, _, err := OpenStore("s3://bucket/prefix")
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Errorf("an unsupported backend is a feature request: %v", err)
	}
	// r2:// without credentials in the environment says which variables it
	// wants, and never suggests a flag.
	t.Setenv(R2AccountEnv, "acct")
	t.Setenv(R2KeyEnv, "")
	t.Setenv(R2SecretEnv, "")
	_, _, err = OpenStore("r2://bucket/prefix")
	if err == nil || !strings.Contains(err.Error(), R2KeyEnv) {
		t.Errorf("r2 without credentials: %v", err)
	}
	t.Setenv(R2KeyEnv, "k")
	t.Setenv(R2SecretEnv, "SECRET-1a2b3c")
	_, where, err := OpenStore("r2://bucket/prefix/")
	if err != nil || where != "r2://bucket/prefix" {
		t.Errorf("r2 with credentials: %q, %v", where, err)
	}
	if _, _, err := OpenStore("r2://"); err == nil {
		t.Error("r2 with no bucket was accepted")
	}
}

func TestCheckName(t *testing.T) {
	for _, name := range []string{"spec.md", "api.proto", "flow.mmd", "a b.md", "x.yaml"} {
		if err := CheckName(name); err != nil {
			t.Errorf("CheckName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "../x.md", ".env", "a/b.md", `a\b.md`, "..", " x.md", "x.md\n", strings.Repeat("a", 200) + ".md"} {
		if err := CheckName(name); err == nil {
			t.Errorf("CheckName(%q) accepted", name)
		}
	}
}

// The MCP transport is mounted with its auth; the tools arrive with #96.
func TestMCPAndHealthAreMounted(t *testing.T) {
	_, ts := open(t, storage.NewMemory())
	if code, body := get(t, ts.URL+"/healthz"); code != 200 || !strings.HasPrefix(body, "ok") {
		t.Errorf("/healthz = %d %q", code, body)
	}
	res, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("/mcp without the token = %d", res.StatusCode)
	}
	if code, _ := get(t, ts.URL+"/"); code != 200 {
		t.Errorf("/ = %d", code)
	}
	if code, _ := get(t, ts.URL+"/nothing"); code != http.StatusNotFound {
		t.Errorf("/nothing = %d, want 404", code)
	}
}

// On R2 every call is billed, so the cost of each path is pinned: after
// startup, serving the inbox and a review costs nothing; a comment is exactly
// one PUT; waiting in await_review_done makes no call at all; a publish is
// one PUT per document plus one for the metadata (and the one-key listing
// that keeps a blob off a log's key).
func TestR2CostsOnePutPerCommentAndNothingToServe(t *testing.T) {
	r, f := fakeR2WithCounts(t)
	h, ts := open(t, r)
	a := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	if got := f.Count("PUT"); got != 2 {
		t.Errorf("a one-document publish cost %d PUTs, want 2 (document + metadata)", got)
	}
	// A fresh process: the startup listing and reads happen here, once.
	ts.Close()
	h2, ts2 := open(t, r)
	f.Reset()
	for i := 0; i < 3; i++ {
		if code, _ := get(t, ts2.URL+"/"); code != 200 {
			t.Fatal(code)
		}
		if code, _ := get(t, ts2.URL+"/r/"+a.Meta.ID+"/"); code != 200 {
			t.Fatal(code)
		}
		if code, _ := get(t, ts2.URL+"/api/reviews"); code != 200 {
			t.Fatal(code)
		}
	}
	if got := f.Total(); got != 0 {
		t.Errorf("serving the inbox and a review cost %d R2 calls after startup, want 0", got)
	}
	if code := post(t, ts2.URL+"/r/"+a.Meta.ID+"/api/feedback", feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "why?", Author: "b"}); code != http.StatusCreated {
		t.Fatal(code)
	}
	if got, total := f.Count("PUT"), f.Total(); got != 1 || total != 1 {
		t.Errorf("a comment cost %d PUTs and %d calls in all, want exactly 1 and 1", got, total)
	}
	f.Reset()
	cs := connect(t, ts2)
	var waited awaitOut
	if err := call(t, cs, "await_review_done", map[string]any{"review_id": a.Meta.ID, "timeout_seconds": 2}, &waited); err != nil {
		t.Fatal(err)
	}
	if waited.Done || waited.Reason != "timeout" {
		t.Fatalf("await: %+v", waited)
	}
	if got := f.Total(); got != 0 {
		t.Errorf("waiting cost %d R2 calls, want 0: the wait polls memory, never the store", got)
	}
	// And the comment survived a restart, read back from the one object it
	// was written as.
	ts2.Close()
	h3, ts3 := open(t, r)
	if _, body := get(t, ts3.URL+"/r/"+a.Meta.ID+"/api/feedback"); !strings.Contains(body, "why?") {
		t.Error("the comment did not come back from R2 after a restart")
	}
	_, _ = h2, h3
}
