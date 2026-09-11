package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/diagram"
	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// drawnSVG is the shape of mermaid-cli's output for the fence in fenceDoc:
// enough of it that anchoring has something to bite on.
const drawnSVG = `<svg id="my-svg" xmlns="http://www.w3.org/2000/svg">` +
	`<path id="my-svg-L_client_api_0" data-id="L_client_api_0"/>` +
	`<path id="my-svg-L_api_queue_0" data-id="L_api_queue_0"/>` +
	`<path id="my-svg-L_worker_queue_0" data-id="L_worker_queue_0"/>` +
	`<g class="node default" id="my-svg-flowchart-client-0"><rect/></g>` +
	`</svg>`

// stubRenderer stands in for mermaid-cli: the real one starts a headless
// browser, which a test must not depend on.
func stubRenderer(t *testing.T) *diagram.Renderer {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	dir := t.TempDir()
	body := filepath.Join(dir, "body.svg")
	if err := os.WriteFile(body, []byte(drawnSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "mmdc")
	script := "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) out=\"$2\"; shift 2;;\n    *) shift;;\n  esac\ndone\ncat " + body + " > \"$out\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &diagram.Renderer{Bin: bin, Cache: filepath.Join(dir, "cache")}
}

// newDrawnServer serves the markdown fence with a renderer attached.
func newDrawnServer(t *testing.T, r *diagram.Renderer) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diagram.md")
	if err := os.WriteFile(path, []byte(fenceDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Doc: doc, Store: feedback.NewStore(path), Author: "tester", Diagrams: r}
	return New(opts), path
}

func TestPageInlinesTheDrawnDiagram(t *testing.T) {
	s, _ := newDrawnServer(t, stubRenderer(t))
	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, `<figure class="diagram">`) {
		t.Fatal("the page shows no drawn diagram")
	}
	if !strings.Contains(body, `<svg id="my-svg"`) {
		t.Error("the SVG was not inlined")
	}
	// The picture is the review surface: its shapes carry the statements'
	// own block ids.
	for _, want := range []string{
		`data-anchor="1/2/client-->api"`,
		`data-anchor="1/2/worker-->queue"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the picture is missing %s", want)
		}
	}
	// And the source is still there, anchored, one tap away.
	if !strings.Contains(body, `data-block="1/2/client--&gt;api"`) {
		t.Error("the anchored source is gone")
	}
	if !strings.Contains(body, `class="flip"`) {
		t.Error("no toggle between picture and source")
	}
	// Nothing on the page may be fetched: an SVG namespace is a name, a
	// src or href is a request.
	for _, bad := range []string{`src="http`, `href="http`, "url(http", "@import"} {
		if strings.Contains(body, bad) {
			t.Errorf("the page fetches something: %s", bad)
		}
	}
}

// Without mermaid-cli the review still works: the reviewer reads the anchored
// source, which is what they had before diagrams were drawn at all.
func TestPageWithoutARendererShowsSource(t *testing.T) {
	s, _ := newDrawnServer(t, nil)
	body := get(t, s, "/").Body.String()
	if strings.Contains(body, `<figure class="diagram">`) {
		t.Fatal("drew a diagram with no renderer")
	}
	if !strings.Contains(body, `data-block="1/2/client--&gt;api"`) {
		t.Error("the anchored source is gone")
	}
	if !s.hasDiagrams() {
		t.Error("the server does not notice the document has a diagram")
	}
}

// A note posted from a shape is a note on that statement — the same event the
// source would have produced, since both surfaces carry the same anchor.
func TestFeedbackFromAShapeLandsOnTheStatement(t *testing.T) {
	s, path := newDrawnServer(t, stubRenderer(t))
	hash, quote := hashOf(t, s, "/api/doc", "1/2/worker-->queue")
	event := map[string]string{
		"doc": path, "block": "1/2/worker-->queue", "quote": quote, "hash": hash,
		"type": "comment", "text": "retry has no backoff", "author": "tester",
	}
	if res := post(t, s, "/api/feedback", event); res.Code != 201 {
		t.Fatalf("post: %d %s", res.Code, res.Body.String())
	}
	log, err := os.ReadFile(path + ".feedback.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json escapes the > of the arrow; the block is the same one.
	if !strings.Contains(string(log), `"block":"1/2/worker--\u003equeue"`) {
		t.Fatalf("the note did not land on the statement:\n%s", log)
	}
}

// A re-parse throws the rendered SVG away with the old blocks, so a watched
// document has to be drawn again — otherwise the first save turns every
// picture back into text.
func TestRescanRedrawsDiagrams(t *testing.T) {
	r := stubRenderer(t)
	s, path := newDrawnServer(t, r)
	seen := map[string]stamp{}
	for p, st := range s.stamps {
		seen[p] = st
	}
	if err := os.WriteFile(path, []byte(fenceDoc+"\nOne more line.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.rescan(seen)
	if s.Revision() == 0 {
		t.Fatal("the edit was not noticed")
	}
	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, `<figure class="diagram">`) {
		t.Fatal("the re-parse lost the picture")
	}
	if !strings.Contains(body, `data-anchor="1/2/client-->api"`) {
		t.Error("the re-drawn picture is not anchored")
	}
}
