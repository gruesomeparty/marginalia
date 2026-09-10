package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// newSetServer serves three documents, one of them in a subdirectory, the way
// `marginalia serve <dir>` would.
func newSetServer(t *testing.T) (*Server, map[string]*feedback.Store) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"spec.md":      "# Spec\n\nBody.\n",
		"api.proto":    "message M {\n  string a = 1;\n}\n",
		"docs/plan.md": "# Plan\n",
	}
	var entries []Entry
	stores := map[string]*feedback.Store{}
	for _, rel := range []string{"spec.md", "api.proto", "docs/plan.md"} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(files[rel]), 0o644); err != nil {
			t.Fatal(err)
		}
		doc, err := document.Parse(full)
		if err != nil {
			t.Fatal(err)
		}
		store := feedback.NewStore(full)
		stores[rel] = store
		entries = append(entries, Entry{Doc: doc, Store: store, Label: rel, Rel: rel})
	}
	opts := Options{Docs: entries, Root: root, Title: "Handover", Nested: true, Author: "tester"}
	return New(opts), stores
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr
}

func post(t *testing.T, s *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", path, bytes.NewReader(buf)))
	return rr
}

func TestSetIndexServesFirstDocumentWithNav(t *testing.T) {
	s, _ := newSetServer(t)
	rr := get(t, s, "/")
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Handover", `href="/d/api.proto"`, `href="/d/docs/plan.md"`, "3 documents · 0 done", "Finish review set"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(body, `aria-current="page"`) {
		t.Error("the served document should be marked current in the nav")
	}
}

func TestSetServesEachDocumentAtItsOwnURL(t *testing.T) {
	s, _ := newSetServer(t)
	rr := get(t, s, "/d/docs/plan.md")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Plan") {
		t.Fatalf("code=%d body missing the document", rr.Code)
	}
	if rr := get(t, s, "/d/nope.md"); rr.Code != http.StatusNotFound {
		t.Errorf("unknown document: code=%d, want 404", rr.Code)
	}
}

func TestSetAPIDocSelectsByDocument(t *testing.T) {
	s, _ := newSetServer(t)
	rr := get(t, s, "/api/doc?doc=api.proto")
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	var payload struct {
		Doc  *document.Document  `json:"doc"`
		Docs []map[string]string `json:"docs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Doc.Format != document.FormatProto {
		t.Errorf("format = %q, want the requested document", payload.Doc.Format)
	}
	// The whole set is discoverable, so an agent reading the API knows what
	// the human was handed, not just the document it asked about.
	if len(payload.Docs) != 3 {
		t.Errorf("docs = %+v, want all three", payload.Docs)
	}
	if rr := get(t, s, "/api/doc?doc=nope.md"); rr.Code != http.StatusNotFound {
		t.Errorf("unknown document: code=%d, want 404", rr.Code)
	}
	if rr := get(t, s, "/api/feedback?doc=nope.md"); rr.Code != http.StatusNotFound {
		t.Errorf("unknown document feedback: code=%d, want 404", rr.Code)
	}
}

// Feedback is routed to the document it is about, each with its own log.
func TestSetFeedbackRoutesToItsOwnLog(t *testing.T) {
	s, stores := newSetServer(t)
	target := stores["api.proto"]
	rr := post(t, s, "/api/feedback", feedback.Event{
		Doc:   "api.proto",
		Block: "M/a",
		Type:  feedback.TypeReject,
		Text:  "field 1 is taken",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got, err := target.Load()
	if err != nil || len(got) != 1 || got[0].Block != "M/a" {
		t.Fatalf("api.proto log = %+v (%v)", got, err)
	}
	// The event records the document's real path, not the URL it arrived as.
	if !strings.HasSuffix(got[0].Doc, "api.proto") || got[0].Doc == "api.proto" {
		t.Errorf("event doc = %q, want the document's path", got[0].Doc)
	}
	for rel, store := range stores {
		if rel == "api.proto" {
			continue
		}
		if events, _ := store.Load(); len(events) != 0 {
			t.Errorf("%s should be untouched, got %+v", rel, events)
		}
	}
}

// A stale page must not be able to append to a file outside the review set.
func TestSetFeedbackRejectsUnknownDocument(t *testing.T) {
	s, _ := newSetServer(t)
	rr := post(t, s, "/api/feedback", feedback.Event{
		Doc:  "/etc/passwd",
		Type: feedback.TypeComment,
		Text: "nope",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", rr.Code)
	}
	rr = post(t, s, "/api/feedback?doc=nope.md", feedback.Event{Type: feedback.TypeComment, Text: "nope"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("query form: code=%d, want 400", rr.Code)
	}
}

// Finishing the set is the agent's signal to proceed, so it must land in every
// log — an agent watching any one document sees the handover close.
func TestSessionDoneMarksEveryDocument(t *testing.T) {
	s, stores := newSetServer(t)
	rr := post(t, s, "/api/session_done", nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	for rel, store := range stores {
		events, err := store.Load()
		if err != nil || len(events) != 1 {
			t.Fatalf("%s log = %+v (%v)", rel, events, err)
		}
		if events[0].Type != feedback.TypeReviewDone || events[0].Text != "session" {
			t.Errorf("%s event = %+v, want a session review_done", rel, events[0])
		}
		if events[0].Author != "tester" || events[0].Ts == "" {
			t.Errorf("%s event missing author/timestamp: %+v", rel, events[0])
		}
	}
}

// The sidebar's counts and ticks are read from disk on every render, so a
// reviewer who reloads still sees where they are.
func TestSetNavShowsProgress(t *testing.T) {
	s, stores := newSetServer(t)
	_ = stores["spec.md"].Append(feedback.Event{Block: "1/1", Type: feedback.TypeComment, Text: "note"})
	_ = stores["docs/plan.md"].Append(feedback.Event{Type: feedback.TypeReviewDone})
	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, "3 documents · 1 done") {
		t.Error("nav summary should count the finished document")
	}
	if strings.Count(body, `class="tick"`) != 3 || !strings.Contains(body, `<span class="tick" title="Marked done">`) {
		t.Error("the done document should show an unhidden tick")
	}
	if !strings.Contains(body, `<span class="n">1</span>`) {
		t.Errorf("the commented document should show a count badge")
	}
}

// The revision loop's API: after a document is edited, the resolution says
// which notes still apply, which the edit outran, and which lost their block.
func TestResolutionAfterRevision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "spec.md")
	first := "# Spec\n\nCapped at 500.\n\nGone soon.\n"
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	store := feedback.NewStore(path)
	hashOf := func(d *document.Document, id string) string {
		for _, b := range d.Blocks {
			if b.ID == id {
				return b.Hash
			}
		}
		t.Fatalf("no block %s", id)
		return ""
	}
	for _, e := range []feedback.Event{
		{Block: "1/1", Quote: "Spec", Hash: hashOf(doc, "1/1"), Type: feedback.TypeApprove, Ts: "2026-07-03T10:00:00Z"},
		{Block: "1/2", Quote: "Capped at 500.", Hash: hashOf(doc, "1/2"), Type: feedback.TypeReject, Text: "too low", Ts: "2026-07-03T10:01:00Z"},
		{Block: "1/3", Quote: "Gone soon.", Hash: hashOf(doc, "1/3"), Type: feedback.TypeQuestion, Text: "why?", Ts: "2026-07-03T10:02:00Z"},
	} {
		if err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	// The author revises: one block edited, one deleted.
	if err := os.WriteFile(path, []byte("# Spec\n\nCapped at 5000.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	revised, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Doc: revised, Store: store, Author: "tester"})

	rr := get(t, s, "/api/resolution")
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	var res feedback.Resolution
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Comments != 3 || res.Stale != 1 || res.Orphaned != 1 {
		t.Fatalf("resolution = %+v", res)
	}
	byBlock := map[string]feedback.State{}
	for _, st := range res.States {
		byBlock[st.Block] = st
	}
	if st := byBlock["1/1"]; st.Stale || st.Orphaned || st.Current.Event.Type != feedback.TypeApprove {
		t.Errorf("untouched block = %+v", st)
	}
	if st := byBlock["1/2"]; !st.Stale || st.Orphaned {
		t.Errorf("edited block should be stale: %+v", st)
	}
	if st := byBlock["1/3"]; !st.Orphaned || st.Current.Event.Quote != "Gone soon." {
		t.Errorf("deleted block's note should be surfaced with its quote: %+v", st)
	}

	// The page says the same thing, including the unanchored note.
	body := get(t, s, "/").Body.String()
	for _, want := range []string{"1 note no longer anchored", "Gone soon.", `class="orphans"`, `"stale":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestResolutionSelectsDocumentAndReportsErrors(t *testing.T) {
	s, stores := newSetServer(t)
	_ = stores["api.proto"].Append(feedback.Event{Block: "M/a", Type: feedback.TypeComment, Text: "hi", Ts: "2026-07-03T10:00:00Z"})
	rr := get(t, s, "/api/resolution?doc=api.proto")
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	var res feedback.Resolution
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Comments != 1 || len(res.States) != 1 || res.States[0].Block != "M/a" {
		t.Errorf("resolution = %+v", res)
	}
	if rr := get(t, s, "/api/resolution?doc=nope.md"); rr.Code != http.StatusNotFound {
		t.Errorf("unknown document: code=%d, want 404", rr.Code)
	}
	if err := os.WriteFile(stores["spec.md"].Path(), []byte("{not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rr := get(t, s, "/api/resolution"); rr.Code != http.StatusInternalServerError {
		t.Errorf("corrupt log: code=%d, want 500", rr.Code)
	}
}

// An oversized body must be refused rather than read into memory, even on a
// trusted interface.
func TestPostFeedbackRefusesOversizedBody(t *testing.T) {
	s, stores := newSetServer(t)
	huge := bytes.NewReader([]byte(`{"type":"comment","text":"` + strings.Repeat("x", maxFeedbackBody+1) + `"}`))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/feedback", huge))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code=%d, want 413", rr.Code)
	}
	for rel, store := range stores {
		if events, _ := store.Load(); len(events) != 0 {
			t.Errorf("%s should be untouched, got %+v", rel, events)
		}
	}
	// A body under the cap still works.
	rr = post(t, s, "/api/feedback", feedback.Event{Block: "1/1", Type: feedback.TypeComment, Text: "fine"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// A failure discovered while rendering must not arrive as a 200 with half a
// document in it.
func TestRenderFailureIsNotAPartialPage(t *testing.T) {
	s, stores := newSetServer(t)
	if err := os.WriteFile(stores["spec.md"].Path(), []byte("{not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := get(t, s, "/")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d, want 500", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "<!doctype") || strings.Contains(rr.Body.String(), "window.__MARGINALIA__") {
		t.Errorf("500 response leaked page markup: %q", rr.Body.String())
	}
}
