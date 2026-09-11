package diagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
)

const fenceDoc = "# Flow\n\n```mermaid\nflowchart LR\n  client --> api\n  subgraph payments\n    charge --> invoice\n  end\n  worker -.retry.-> queue\n```\n\nProse after it.\n"

func parsed(t *testing.T, name, body string) *document.Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func blockByID(t *testing.T, doc *document.Document, id string) *document.Block {
	t.Helper()
	for i := range doc.Blocks {
		if doc.Blocks[i].ID == id {
			return &doc.Blocks[i]
		}
	}
	t.Fatalf("no block %q", id)
	return nil
}

func TestAttachDrawsAFenceAndAnchorsIt(t *testing.T) {
	doc := parsed(t, "flow.md", fenceDoc)
	bin, runs := fakeMMDC(t, realSVG)
	rendered, failures := Attach(doc, &Renderer{Bin: bin, Cache: t.TempDir()})
	if rendered != 1 || len(failures) != 0 {
		t.Fatalf("rendered %d, failures %v", rendered, failures)
	}
	if got := countRuns(t, runs); got != 1 {
		t.Fatalf("ran the renderer %d times for one diagram", got)
	}
	fence := blockByID(t, doc, "1/2")
	if fence.SVG == "" {
		t.Fatal("the fence carries no picture")
	}
	// The statements the document wrote are the picture's anchors, nested
	// ones included.
	for _, want := range []string{
		`data-anchor="1/2/client-->api"`,
		`data-anchor="1/2/worker-->queue"`,
		`data-anchor="1/2/payments"`,
		`data-anchor="1/2/payments/charge-->invoice"`,
	} {
		if !strings.Contains(fence.SVG, want) {
			t.Errorf("missing %s in the drawn diagram", want)
		}
	}
	// The source stays exactly as it was: the picture is an addition, not a
	// replacement, and the anchored source is what the toggle shows.
	if !strings.Contains(fence.HTML, `data-block="1/2/client--&gt;api"`) {
		t.Error("the anchored source was lost")
	}
	// Nothing else in the document acquires a picture.
	if blockByID(t, doc, "1/1").SVG != "" {
		t.Error("drew a diagram for the heading")
	}
}

func TestAttachDrawsAMermaidDocument(t *testing.T) {
	doc := parsed(t, "flow.mmd", "flowchart LR\n  client --> api\n  worker -.retry.-> queue\n")
	bin, _ := fakeMMDC(t, realSVG)
	rendered, failures := Attach(doc, &Renderer{Bin: bin, Cache: t.TempDir()})
	if rendered != 1 || len(failures) != 0 {
		t.Fatalf("rendered %d, failures %v", rendered, failures)
	}
	head := doc.Blocks[0]
	if head.SVG == "" {
		t.Fatalf("the diagram document carries no picture (block %q, kind %q)", head.ID, head.Kind)
	}
	if !strings.Contains(head.SVG, `data-anchor="client-->api"`) {
		t.Errorf("statements of a .mmd document are not anchored:\n%s", head.SVG)
	}
}

// A renderer that fails leaves the document exactly as it was: the reviewer
// reads the anchored source, which is what they had before diagrams existed.
func TestAttachFailureKeepsTheSource(t *testing.T) {
	doc := parsed(t, "flow.md", fenceDoc)
	rendered, failures := Attach(doc, &Renderer{Bin: failingMMDC(t, "boom"), Cache: t.TempDir()})
	if rendered != 0 {
		t.Fatalf("rendered %d despite a failing renderer", rendered)
	}
	if len(failures) != 1 || !strings.Contains(failures[0].Error(), "boom") {
		t.Fatalf("failure not reported: %v", failures)
	}
	fence := blockByID(t, doc, "1/2")
	if fence.SVG != "" {
		t.Error("a failed render left a picture behind")
	}
	if !strings.Contains(fence.HTML, `data-block="1/2/client--&gt;api"`) {
		t.Error("the anchored source was lost")
	}
}

func TestAttachWithoutARendererIsANoop(t *testing.T) {
	doc := parsed(t, "flow.md", fenceDoc)
	if rendered, failures := Attach(doc, &Renderer{}); rendered != 0 || failures != nil {
		t.Fatalf("rendered %d, failures %v", rendered, failures)
	}
	if blockByID(t, doc, "1/2").SVG != "" {
		t.Error("drew something without a renderer")
	}
	if rendered, _ := Attach(nil, &Renderer{Bin: "x"}); rendered != 0 {
		t.Error("a nil document rendered something")
	}
}

func TestAttachSkipsADocumentWithNoDiagram(t *testing.T) {
	doc := parsed(t, "plain.md", "# Title\n\nSome prose.\n\n```go\nfunc main() {}\n```\n")
	bin, runs := fakeMMDC(t, realSVG)
	rendered, failures := Attach(doc, &Renderer{Bin: bin, Cache: t.TempDir()})
	if rendered != 0 || failures != nil {
		t.Fatalf("rendered %d, failures %v", rendered, failures)
	}
	if got := countRuns(t, runs); got != 0 {
		t.Fatalf("started the renderer %d times for a document with no diagram", got)
	}
}
