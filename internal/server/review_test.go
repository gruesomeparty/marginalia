package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
)

// Two top-level sections, so section 2 is a whole section to hand over
// read-only: blocks 1/1, 1/2, 2/1, 2/2.
const framedDoc = "# Spec\n\nCapped at 500.\n\n# Context\n\nBackground, not up for review.\n"

// framed serves one document under a configured review.
func framed(t *testing.T, cfg *review.Config) (*Server, *feedback.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(framedDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	store := feedback.NewStore(path)
	return New(Options{Doc: doc, Store: store, Author: "tester", Review: cfg}), store
}

// config parses YAML the way `serve --config` would, so the tests exercise the
// same path an agent uses.
func config(t *testing.T, body string) *review.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "review.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := review.Load(path)
	if err != nil {
		t.Fatalf("review.Load: %v", err)
	}
	return cfg
}

// The acceptance criterion of issue #3: a configured action is offered by the
// page and lands in the log as its own type, with no composer step.
func TestConfiguredActionIsOfferedAndAccepted(t *testing.T) {
	cfg := config(t, "instructions: Focus on the cap.\nactions:\n  - type: blocker\n    label: Blocker\n    key: b\n")
	s, store := framed(t, cfg)

	page := get(t, s, "/").Body.String()
	if !strings.Contains(page, "Focus on the cap.") {
		t.Error("the page should show what the agent asked for")
	}
	for _, want := range []string{`"type":"blocker"`, `"label":"Blocker"`, `"key":"b"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page payload is missing %s", want)
		}
	}
	rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: "blocker"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "blocker" || events[0].Block != "1/2" {
		t.Fatalf("log = %+v", events)
	}
	// A one-tap verdict carries no text, and that is not a validation failure.
	if events[0].Text != "" {
		t.Errorf("text = %q, want empty", events[0].Text)
	}
}

// Rendering the rules is not enforcing them: the server refuses vocabulary the
// agent never configured, whatever a stale page posts.
func TestServerRefusesUnconfiguredVocabulary(t *testing.T) {
	s, store := framed(t, config(t, "actions:\n  - type: blocker\n"))
	for _, typ := range []string{"bogus", "BLOCKER", "nit"} {
		rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: typ})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("type %q: code=%d, want 400", typ, rr.Code)
		}
	}
	// And with the built-ins off, yesterday's vocabulary is gone too.
	off, _ := framed(t, config(t, "builtins: false\nactions:\n  - type: blocker\n"))
	if rr := post(t, off, "/api/feedback", feedback.Event{Block: "1/2", Type: "comment"}); rr.Code != http.StatusBadRequest {
		t.Errorf("comment with builtins off: code=%d, want 400", rr.Code)
	}
	if events, _ := store.Load(); len(events) != 0 {
		t.Errorf("nothing should have been written: %+v", events)
	}
}

func TestStructuredFieldsRoundTrip(t *testing.T) {
	cfg := config(t, `
actions:
  - type: finding
    requires_text: true
    fields:
      - name: severity
        options: [high, low]
        required: true
      - name: owner
`)
	s, store := framed(t, cfg)
	rr := post(t, s, "/api/feedback", feedback.Event{
		Block: "1/2", Type: "finding", Text: "the cap is wrong",
		Fields: map[string]string{"severity": "high", "owner": "berkay"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Fields["severity"] != "high" || events[0].Fields["owner"] != "berkay" {
		t.Fatalf("log = %+v", events)
	}
	// A value outside the configured choices, a missing required field and a
	// field that was never declared are all refused.
	bad := []feedback.Event{
		{Block: "1/2", Type: "finding", Text: "x", Fields: map[string]string{"severity": "urgent"}},
		{Block: "1/2", Type: "finding", Text: "x"},
		{Block: "1/2", Type: "finding", Text: "x", Fields: map[string]string{"severity": "low", "sev": "high"}},
		{Block: "1/2", Type: "finding", Fields: map[string]string{"severity": "low"}},
	}
	for i, e := range bad {
		if rr := post(t, s, "/api/feedback", e); rr.Code != http.StatusBadRequest {
			t.Errorf("bad event %d: code=%d, want 400", i, rr.Code)
		}
	}
	if events, _ = store.Load(); len(events) != 1 {
		t.Errorf("only the valid event should be on disk: %+v", events)
	}
}

// A block handed over read-only is context: the page does not open a composer
// on it and the server will not write to it either.
func TestReadOnlyBlocks(t *testing.T) {
	s, store := framed(t, config(t, "readonly:\n  - \"2\"\n"))
	rr := post(t, s, "/api/feedback", feedback.Event{Block: "2/1", Type: feedback.TypeComment, Text: "no"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s, want 403", rr.Code, rr.Body.String())
	}
	if events, _ := store.Load(); len(events) != 0 {
		t.Fatalf("nothing should have been written: %+v", events)
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: feedback.TypeComment, Text: "yes"}); rr.Code != http.StatusCreated {
		t.Fatalf("a commentable block still takes notes: %d", rr.Code)
	}
	// The page is told which blocks those are, since an inline block's markup
	// is written by the parser and cannot carry the flag itself.
	body := get(t, s, "/api/doc").Body.Bytes()
	var doc struct {
		Doc    *document.Document `json:"doc"`
		Review struct {
			ReadOnly []string `json:"readonly"`
		} `json:"review"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Review.ReadOnly) != 1 || doc.Review.ReadOnly[0] != "2" {
		t.Errorf("api should describe the review: %+v", doc.Review)
	}
	if !strings.Contains(get(t, s, "/").Body.String(), `"readonly":true`) {
		t.Error("the page payload should mark the read-only blocks")
	}
}

// require_verdict withholds review_done until every commentable block has
// been answered — and says how many are left, so the page can too.
func TestRequireVerdict(t *testing.T) {
	s, store := framed(t, config(t, "require_verdict: true\nreadonly:\n  - \"2\"\n"))
	rr := post(t, s, "/api/feedback", feedback.Event{Type: feedback.TypeReviewDone})
	if rr.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s, want 409", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "still to go") {
		t.Errorf("the refusal should say what is left: %q", rr.Body.String())
	}
	// Section 2 is read-only, so only section 1's blocks need answering.
	for _, block := range []string{"1/1", "1/2"} {
		if rr := post(t, s, "/api/feedback", feedback.Event{Block: block, Type: feedback.TypeApprove}); rr.Code != http.StatusCreated {
			t.Fatalf("approve %s: %d", block, rr.Code)
		}
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Type: feedback.TypeReviewDone}); rr.Code != http.StatusCreated {
		t.Fatalf("done should be accepted now: %d %s", rr.Code, rr.Body.String())
	}
	if !hasReviewDone(mustEvents(t, store)) {
		t.Error("review_done not written")
	}
}

// The set-wide finish is the same gate: it must not mark documents done that
// the review says are unanswered, and it must not half-write the set.
func TestSessionDoneRespectsRequireVerdict(t *testing.T) {
	s, stores := newSetServer(t)
	s.opts.Review = config(t, "require_verdict: true\n")
	rr := post(t, s, "/api/session_done", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s, want 409", rr.Code, rr.Body.String())
	}
	for rel, store := range stores {
		if events := mustEvents(t, store); len(events) != 0 {
			t.Errorf("%s should be untouched: %+v", rel, events)
		}
	}
}

func mustEvents(t *testing.T, store *feedback.Store) []feedback.Event {
	t.Helper()
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// Without a config the review is exactly what it always was.
func TestDefaultReviewUnchanged(t *testing.T) {
	s, _, _ := newTestServer(t)
	page := get(t, s, "/").Body.String()
	for _, want := range []string{`"type":"comment"`, `"type":"suggest_edit"`, `"type":"approve"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page payload is missing the built-in %s", want)
		}
	}
	if strings.Contains(page, `class="framing"`) {
		t.Error("no config, no banner")
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/1", Type: "blocker"}); rr.Code != http.StatusBadRequest {
		t.Errorf("an unconfigured type: code=%d, want 400", rr.Code)
	}
}

// A skipped block folds on the page and takes no feedback, and the API says
// which blocks those are so a consuming agent knows what was deliberately
// not reviewed.
func TestSkippedBlocks(t *testing.T) {
	cfg := config(t, "skip:\n  - \"2\"\nnotes:\n  - block: \"1/2\"\n    text: why 500?\n")
	s, store := framed(t, cfg)
	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "2/2", Type: feedback.TypeComment, Text: "no"}); rr.Code != http.StatusForbidden {
		t.Errorf("a skipped block should refuse feedback: %d", rr.Code)
	}
	if events, _ := store.Load(); len(events) != 0 {
		t.Errorf("nothing should have been written: %+v", events)
	}
	var doc struct {
		ReadOnly []string `json:"readonly"`
		Skipped  []string `json:"skipped"`
		Review   struct {
			Notes []review.Note `json:"notes"`
			Skip  []string      `json:"skip"`
		} `json:"review"`
	}
	if err := json.Unmarshal(get(t, s, "/api/doc").Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if strings.Join(doc.Skipped, ",") != "2/1,2/2" {
		t.Errorf("skipped blocks = %v", doc.Skipped)
	}
	// Skipped implies read-only, so the same blocks appear there.
	if strings.Join(doc.ReadOnly, ",") != "2/1,2/2" {
		t.Errorf("read-only blocks = %v", doc.ReadOnly)
	}
	if len(doc.Review.Notes) != 1 || doc.Review.Notes[0].Block != "1/2" {
		t.Errorf("the requester's notes are not discoverable: %+v", doc.Review.Notes)
	}
	page := get(t, s, "/").Body.String()
	if !strings.Contains(page, `"skipped":true`) {
		t.Error("the page payload should mark skipped blocks")
	}
	if !strings.Contains(page, "why 500?") {
		t.Error("the page should carry the requester's note")
	}
	// And the note stays out of the log: it is the agent asking, not the
	// human answering.
	if events, _ := store.Load(); len(events) != 0 {
		t.Errorf("requester notes must never be events: %+v", events)
	}
}
