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
)

const fenceDoc = "# Ingest architecture\n" +
	"\n" +
	"```mermaid\n" +
	"flowchart LR\n" +
	"  client[Client] --> api[Ingest API]\n" +
	"  api --> queue[(Queue)]\n" +
	"  worker -.retry.-> queue\n" +
	"```\n"

const standaloneDiagram = "flowchart LR\n  client[Client] --> api[Ingest API]\n  worker -.retry.-> queue\n"

// newDiagramServer serves a markdown document with a mermaid fence and a
// standalone .mmd diagram, the way `marginalia serve <dir>` would.
func newDiagramServer(t *testing.T) (*Server, map[string]*feedback.Store) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"diagram.md": fenceDoc, "flow.mmd": standaloneDiagram}
	var entries []Entry
	stores := map[string]*feedback.Store{}
	for _, rel := range []string{"diagram.md", "flow.mmd"} {
		full := filepath.Join(root, rel)
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
	return New(Options{Docs: entries, Root: root, Title: "Diagrams", Author: "tester"}), stores
}

// hashOf is the anchor hash the page would post for a block.
func hashOf(t *testing.T, s *Server, path, block string) (hash, quote string) {
	t.Helper()
	var payload struct {
		Doc *document.Document `json:"doc"`
	}
	if err := json.Unmarshal(get(t, s, path).Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, b := range payload.Doc.Blocks {
		if b.ID == block {
			return b.Hash, b.Quote
		}
	}
	t.Fatalf("no block %q in %s", block, path)
	return "", ""
}

// The acceptance criterion of issue #25: a comment saved against the retry
// edge lands in the document's log with that edge's path as its block and its
// own hash — not the diagram's.
func TestMermaidFenceEdgeAnchorsFeedback(t *testing.T) {
	s, stores := newDiagramServer(t)
	page := get(t, s, "/d/diagram.md").Body.String()
	for _, want := range []string{
		`data-block="1/2/worker--&gt;queue"`,
		`data-block="1/2/client--&gt;api"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing the anchor %s", want)
		}
	}
	hash, quote := hashOf(t, s, "/api/doc?doc=diagram.md", "1/2/worker-->queue")
	fence, _ := hashOf(t, s, "/api/doc?doc=diagram.md", "1/2")
	if hash == fence {
		t.Error("an edge must hash its own text, not the whole diagram")
	}
	rr := post(t, s, "/api/feedback?doc=diagram.md", feedback.Event{
		Doc: "diagram.md", Block: "1/2/worker-->queue", Quote: quote, Hash: hash,
		Type: feedback.TypeSuggestEdit, Text: "worker -.retry.-> dlq",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	events, err := stores["diagram.md"].Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	got := events[0]
	if got.Block != "1/2/worker-->queue" || got.Hash != hash {
		t.Errorf("event anchored to %q (hash %q), want the retry edge (hash %q)", got.Block, got.Hash, hash)
	}
	if !strings.Contains(got.Quote, "retry") {
		t.Errorf("event quote = %q, want the edge's own text", got.Quote)
	}
	// The materialized view files the note under the edge, so re-rendering
	// shows it against that statement and nowhere else.
	var res feedback.Resolution
	if err := json.Unmarshal(get(t, s, "/api/resolution?doc=diagram.md").Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Orphaned != 0 || res.Stale != 0 {
		t.Errorf("a fresh note should anchor cleanly: %+v", res)
	}
	if len(res.States) != 1 || res.States[0].Block != "1/2/worker-->queue" {
		t.Errorf("states = %+v", res.States)
	}
}

// The same anchoring when the diagram is the document.
func TestMermaidDocumentEdgeAnchorsFeedback(t *testing.T) {
	s, stores := newDiagramServer(t)
	page := get(t, s, "/d/flow.mmd").Body.String()
	if !strings.Contains(page, `data-block="worker--&gt;queue"`) {
		t.Error("page is missing the standalone diagram's edge anchor")
	}
	hash, quote := hashOf(t, s, "/api/doc?doc=flow.mmd", "worker-->queue")
	rr := post(t, s, "/api/feedback?doc=flow.mmd", feedback.Event{
		Doc: "flow.mmd", Block: "worker-->queue", Quote: quote, Hash: hash,
		Type: feedback.TypeComment, Text: "should be a dead-letter queue",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	events, err := stores["flow.mmd"].Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Block != "worker-->queue" || events[0].Hash != hash {
		t.Fatalf("events = %+v", events)
	}
}
