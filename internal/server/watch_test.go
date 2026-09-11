package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// watched serves one document with --watch on, and returns the server, the
// document's path and its store.
func watched(t *testing.T, body string) (*Server, string, *feedback.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	store := feedback.NewStore(path)
	return New(Options{Doc: doc, Store: store, Author: "tester", Watch: true}), path, store
}

// rewrite changes the file and waits for the watcher to notice, so the test
// asserts on the re-parse rather than on a sleep.
func rewrite(t *testing.T, s *Server, path, body string) {
	t.Helper()
	before := s.Revision()
	// mtime has coarse resolution on some filesystems; the size change here
	// makes the stamp differ regardless.
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.Revision() == before {
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not notice the change (revision stuck at %d)", before)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The acceptance criterion of issue #7: an edit is reflected without a
// restart, prior feedback on a changed block comes back stale, and the
// revision advances.
func TestWatchReparsesAndStalesFeedback(t *testing.T) {
	body := "# Spec\n\nCapped at 500.\n"
	s, path, store := watched(t, body)
	hash := ""
	for _, b := range s.docOf(&s.docs[0]).Blocks {
		if b.ID == "1/2" {
			hash = b.Hash
		}
	}
	if err := store.Append(feedback.Event{
		Block: "1/2", Quote: "Capped at 500.", Hash: hash,
		Type: feedback.TypeReject, Text: "too low", Ts: "2026-07-03T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// Before the edit the note stands.
	var res feedback.Resolution
	if err := json.Unmarshal(get(t, s, "/api/resolution").Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Stale != 0 {
		t.Fatalf("nothing should be stale yet: %+v", res)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watch(ctx)
	rewrite(t, s, path, "# Spec\n\nCapped at 2000 records.\n")

	// The re-parse is visible without a restart…
	var payload struct {
		Doc *document.Document `json:"doc"`
	}
	if err := json.Unmarshal(get(t, s, "/api/doc").Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Doc.Blocks[1].PlainText != "Capped at 2000 records." {
		t.Errorf("document not re-parsed: %q", payload.Doc.Blocks[1].PlainText)
	}
	// …and M2 does the rest: the note is now stale.
	if err := json.Unmarshal(get(t, s, "/api/resolution").Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Stale != 1 {
		t.Errorf("edited block's note should be stale: %+v", res)
	}
	// The page can see that what it is showing has moved on.
	var rev struct {
		Rev   uint64 `json:"rev"`
		Watch bool   `json:"watch"`
	}
	if err := json.Unmarshal(get(t, s, "/api/revision").Body.Bytes(), &rev); err != nil {
		t.Fatal(err)
	}
	if rev.Rev == 0 || !rev.Watch {
		t.Errorf("revision = %+v, want a bumped counter and watch:true", rev)
	}
}

// A save that leaves the document unparseable must not blank the page: the
// reviewer keeps the last good render until the next good save.
func TestWatchKeepsLastGoodParse(t *testing.T) {
	s, path, _ := watched(t, "# Spec\n\nBody.\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watch(ctx)

	// Markdown always parses, so break the file the only way that matters:
	// make it unreadable.
	rev := s.Revision()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * watchInterval)
	if s.Revision() != rev {
		t.Error("a vanished file should not count as a new revision")
	}
	if got := s.docOf(&s.docs[0]).Blocks[1].PlainText; got != "Body." {
		t.Errorf("last good parse lost: %q", got)
	}
	if code := get(t, s, "/").Code; code != 200 {
		t.Errorf("page should still render: %d", code)
	}
}

func TestWatchStopsWithContext(t *testing.T) {
	s, path, _ := watched(t, "# Spec\n\nBody.\n")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.watch(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not stop when its context was cancelled")
	}
	// And it really stopped: a later edit changes nothing.
	rev := s.Revision()
	if err := os.WriteFile(path, []byte("# Spec\n\nChanged.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * watchInterval)
	if s.Revision() != rev {
		t.Error("a stopped watcher should not re-parse")
	}
}

// Without --watch nothing polls and the page is told not to.
func TestWithoutWatchNothingChanges(t *testing.T) {
	s, _, _ := newTestServer(t)
	var rev struct {
		Rev   uint64 `json:"rev"`
		Watch bool   `json:"watch"`
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/revision", nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &rev); err != nil {
		t.Fatal(err)
	}
	if rev.Watch || rev.Rev != 0 {
		t.Errorf("revision = %+v, want watch:false and rev 0", rev)
	}
	if body := get(t, s, "/").Body.String(); !strings.Contains(body, `"watch":false`) {
		t.Error("page payload should say watching is off")
	}
}

// The page's baseline must be the revision it was rendered from: a change that
// lands before its first poll would otherwise be adopted as normal.
func TestPageCarriesItsRevision(t *testing.T) {
	s, path, _ := watched(t, "# Spec\n\nBody.\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watch(ctx)
	rewrite(t, s, path, "# Spec\n\nBody, revised.\n")
	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, `"watch":true`) {
		t.Error("page should know watching is on")
	}
	if !strings.Contains(body, `"revision":`+strconv.FormatUint(s.Revision(), 10)) {
		t.Errorf("page should carry the revision it was rendered from (%d)", s.Revision())
	}
}
