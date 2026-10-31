package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// Issue #31's acceptance criterion, end to end: a comment saved against
// Batch/Full in a served Go file lands in handler.go.feedback.jsonl with
// that symbol path and its own hash, and a later edit elsewhere in the file
// leaves the note un-stale.
func TestSourceFileNoteSurvivesEditsElsewhere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handler.go")
	src := "package ingest\n\ntype Batch struct{ Cap int }\n\nfunc (b *Batch) Full() bool { return b.Cap > 0 }\n\nfunc Ingest() {}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	var full document.Block
	for _, b := range doc.Blocks {
		if b.ID == "Batch/Full" {
			full = b
		}
	}
	if full.ID == "" {
		t.Fatalf("no Batch/Full block in %+v", doc.Blocks)
	}
	store := feedback.NewStore(path)
	s := New(Options{Doc: doc, Store: store, Author: "tester", Host: "127.0.0.1"})
	rr := post(t, s, "/api/feedback", feedback.Event{Block: full.ID, Hash: full.Hash, Quote: full.Quote, Type: "comment", Text: "returns before the retry"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	raw, err := os.ReadFile(path + ".feedback.jsonl")
	if err != nil || !strings.Contains(string(raw), `"block":"Batch/Full"`) || !strings.Contains(string(raw), full.Hash) {
		t.Fatalf("log = %s (%v)", raw, err)
	}

	edited := strings.Replace(src, "func Ingest() {}", "func Ingest() { panic(\"todo\") }\n\nconst retries = 3", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err = document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := store.Load()
	var blocks []feedback.Block
	for _, b := range doc.Blocks {
		blocks = append(blocks, feedback.Block{ID: b.ID, Hash: b.Hash, Text: b.PlainText})
	}
	res := feedback.Materialize(events, blocks)
	if res.Stale != 0 || res.Orphaned != 0 || res.Comments != 1 {
		t.Fatalf("after an edit elsewhere: stale %d, orphaned %d, comments %d", res.Stale, res.Orphaned, res.Comments)
	}
}
