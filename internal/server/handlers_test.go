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

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
)

func newTestServer(t *testing.T) (*Server, *feedback.Store, string) {
	t.Helper()
	docPath := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(docPath, []byte("# Title\n\nHello.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(docPath)
	if err != nil {
		t.Fatal(err)
	}
	store := feedback.NewStore(docPath)
	return New(Options{Doc: doc, Store: store, Author: "tester", Host: "127.0.0.1", Port: 0}), store, docPath
}

func TestIndexServesPage(t *testing.T) {
	s, _, _ := newTestServer(t)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "window.__MARGINALIA__") {
		t.Fatalf("code=%d body missing page", rr.Code)
	}
}

func TestPostFeedbackAppendsToDisk(t *testing.T) {
	s, store, _ := newTestServer(t)
	body, _ := json.Marshal(feedback.Event{Block: "1/2", Type: "comment", Text: "note"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/feedback", bytes.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Load()
	if len(got) != 1 || got[0].Text != "note" || got[0].Author != "tester" || got[0].Ts == "" {
		t.Fatalf("disk state wrong: %+v", got)
	}
}

func TestPostFeedbackRejectsBadType(t *testing.T) {
	s, _, _ := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"type": "bogus"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/feedback", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", rr.Code)
	}
}

func TestApiDocRestoresState(t *testing.T) {
	s, store, _ := newTestServer(t)
	_ = store.Append(feedback.Event{Block: "1/1", Type: "comment", Text: "prior", Ts: "2026-07-03T10:00:00Z"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/doc", nil))
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	var payload struct {
		Doc    *document.Document `json:"doc"`
		Events []feedback.Event   `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Doc.Blocks) == 0 || len(payload.Events) != 1 || payload.Events[0].Text != "prior" {
		t.Fatalf("payload wrong: %+v", payload)
	}
}
