package document

import (
	"strings"
	"testing"
)

// The acceptance criterion of issue #18's highlighting item: a fenced ```go
// block renders tokenized spans, and an unknown language renders as plain
// text rather than badly.
func TestFenceHighlighting(t *testing.T) {
	src := "# T\n\n```go\n// why 500\nconst cap = 500\n```\n\n```brainfuck\n++[->+<]\n```\n"
	doc, err := ParseBytes("doc.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	code := doc.Blocks[1]
	if code.Kind != "code" {
		t.Fatalf("block 1 = %+v", code)
	}
	for _, want := range []string{
		`<pre><code class="language-go">`,
		`<span class="tok com">// why 500</span>`,
		`<span class="tok kw">const</span>`,
		`<span class="tok num">500</span>`,
	} {
		if !strings.Contains(code.HTML, want) {
			t.Errorf("missing %s in:\n%s", want, code.HTML)
		}
	}
	// The rendered fence and the block's own anchor quote the same text.
	if got := textOf(code.HTML); !strings.Contains(got, "const cap = 500") {
		t.Errorf("fence renders %q", got)
	}
	if !strings.Contains(code.PlainText, "const cap = 500") {
		t.Errorf("anchor text = %q", code.PlainText)
	}

	other := doc.Blocks[2]
	if strings.Contains(other.HTML, "tok") {
		t.Errorf("an unknown language should not be tokenized:\n%s", other.HTML)
	}
	if !strings.Contains(other.HTML, "++[-&gt;+&lt;]") {
		t.Errorf("unknown language lost its source:\n%s", other.HTML)
	}
}

// A fence with no language tag stays exactly as goldmark renders it.
func TestUntaggedFenceUnchanged(t *testing.T) {
	doc, err := ParseBytes("doc.md", []byte("```\nplain text\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.Blocks[0].HTML, "tok") {
		t.Errorf("untagged fence tokenized:\n%s", doc.Blocks[0].HTML)
	}
	if !strings.Contains(doc.Blocks[0].HTML, "plain text") {
		t.Errorf("untagged fence lost its source:\n%s", doc.Blocks[0].HTML)
	}
}

// Both fence treatments in one document: a mermaid fence anchors its
// statements (#25) and any other fence is tokenized (#18). This is where the
// two features meet, so it is worth pinning: a diagram must not be
// highlighted into an opaque block, and code must not be anchored per line.
func TestMermaidAndHighlightedFencesTogether(t *testing.T) {
	src := "# T\n\n```mermaid\nflowchart LR\n  a --> b\n```\n\n```go\nconst n = 1\n```\n\n```mermaid\nsequenceDiagram\n  A->>B: hi\n```\n"
	doc, err := ParseBytes("doc.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// The diagram's edge is its own block; the Go fence and the sequence
	// diagram are single blocks.
	if got := strings.Join(ids(doc), ","); got != "1/1,1/2,1/2/a-->b,1/3,1/4" {
		t.Fatalf("blocks = %s", got)
	}
	diagram := block(t, doc, "1/2")
	if strings.Contains(diagram.HTML, "tok") {
		t.Errorf("a diagram must not be tokenized:\n%s", diagram.HTML)
	}
	if !strings.Contains(diagram.HTML, `data-block="1/2/a--&gt;b"`) {
		t.Errorf("the diagram lost its anchors:\n%s", diagram.HTML)
	}
	code := block(t, doc, "1/3")
	if !strings.Contains(code.HTML, `<span class="tok kw">const</span>`) {
		t.Errorf("the go fence was not tokenized:\n%s", code.HTML)
	}
	if strings.Contains(code.HTML, "data-block") {
		t.Errorf("code must not be anchored line by line:\n%s", code.HTML)
	}
	// A diagram type we do not take apart keeps its source and gains nothing.
	seq := block(t, doc, "1/4")
	if seq.HasChildren || strings.Contains(seq.HTML, "tok") {
		t.Errorf("unparsed diagram = %+v", seq)
	}
	if !strings.Contains(seq.HTML, "A-&gt;&gt;B: hi") {
		t.Errorf("unparsed diagram lost its source:\n%s", seq.HTML)
	}
}
