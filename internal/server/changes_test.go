package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/changeset"
	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

func changesOf(t *testing.T, s *Server) []changeset.Change {
	t.Helper()
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/changes", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/changes: %d", rr.Code)
	}
	var out struct {
		Changes []changeset.Change `json:"changes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Changes
}

// Under --watch, changes are measured from the parse the reviewer last
// finished: nothing before a Done, the edits after it, nothing again once
// the next Done signs them off.
func TestChangesSinceTheLastDone(t *testing.T) {
	s, _, path := newTestServer(t)
	e := &s.docs[0]
	reparse := func(src string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		doc, err := document.Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		s.setDoc(e, doc)
	}
	reparse("# Title\n\nHello, world.\n")
	if cs := changesOf(t, s); len(cs) != 0 {
		t.Fatalf("no Done yet, changes %+v", cs)
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Type: feedback.TypeReviewDone}); rr.Code != http.StatusCreated {
		t.Fatalf("done: %d", rr.Code)
	}
	reparse("# Title\n\nHello, there.\n\nA second paragraph.\n")
	cs := changesOf(t, s)
	if len(cs) != 2 || cs[0].Kind != changeset.Edited || cs[1].Kind != changeset.Added {
		t.Fatalf("after an edit, changes %+v", cs)
	}
	if len(cs[0].Redline) == 0 {
		t.Error("the edited block has no redline")
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Type: feedback.TypeReviewDone}); rr.Code != http.StatusCreated {
		t.Fatalf("second done: %d", rr.Code)
	}
	if cs := changesOf(t, s); len(cs) != 0 {
		t.Errorf("signed off, changes %+v", cs)
	}
}
