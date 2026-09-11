package document

import (
	"strings"
	"testing"
)

const diagramDoc = "# Ingest architecture\n" +
	"\n" +
	"The flow, end to end:\n" +
	"\n" +
	"```mermaid\n" +
	"flowchart LR\n" +
	"  %% the public entry point\n" +
	"  client[Client] --> api[Ingest API]\n" +
	"  api --> queue[(Queue)]\n" +
	"  queue --> worker{{Worker}}\n" +
	"  worker -.retry.-> queue\n" +
	"  subgraph payments [Payments]\n" +
	"    charge --> refund\n" +
	"  end\n" +
	"```\n" +
	"\n" +
	"Notes below the diagram.\n"

// The point of issue #25: a note about the retry edge lands on that edge, not
// on the fence that happens to contain the diagram.
func TestMermaidFenceStatementsAreTheirOwnBlocks(t *testing.T) {
	doc, err := ParseBytes("diagram.md", []byte(diagramDoc))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id, kind, parent string
		level            int
		inline, kids     bool
	}{
		{"1/1", "heading", "", 1, false, false},
		{"1/2", "paragraph", "", 0, false, false},
		// The fence keeps the block it always had, so a note about the shape
		// of the diagram still anchors.
		{"1/3", "code", "", 0, false, true},
		{"1/3/client-->api", "edge", "1/3", 1, true, false},
		{"1/3/api-->queue", "edge", "1/3", 1, true, false},
		{"1/3/queue-->worker", "edge", "1/3", 1, true, false},
		{"1/3/worker-->queue", "edge", "1/3", 1, true, false},
		{"1/3/payments", "subgraph", "1/3", 1, true, true},
		{"1/3/payments/charge-->refund", "edge", "1/3/payments", 2, true, false},
		{"1/4", "paragraph", "", 0, false, false},
	}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d: %+v", len(doc.Blocks), len(want), ids(doc))
	}
	for i, w := range want {
		got := doc.Blocks[i]
		if got.ID != w.id || got.Kind != w.kind || got.Parent != w.parent ||
			got.Level != w.level || got.Inline != w.inline || got.HasChildren != w.kids {
			t.Errorf("block %d = %+v, want %v", i, got, w)
		}
		if got.Hash == "" || got.Quote == "" {
			t.Errorf("block %s has no anchor: hash=%q quote=%q", got.ID, got.Hash, got.Quote)
		}
	}
}

// Every statement is anchored inside the fence's own markup, and the source
// survives the annotation character for character.
func TestMermaidFenceKeepsItsSource(t *testing.T) {
	doc, err := ParseBytes("diagram.md", []byte(diagramDoc))
	if err != nil {
		t.Fatal(err)
	}
	fence := block(t, doc, "1/3")
	if !strings.HasPrefix(fence.HTML, `<pre><code class="language-mermaid">`) {
		t.Fatalf("fence is not a code block: %s", fence.HTML)
	}
	for _, want := range []string{
		`data-block="1/3/worker--&gt;queue"`,
		`data-kind="subgraph"`,
		`data-parent="1/3/payments"`,
		`data-kids="1"`,
	} {
		if !strings.Contains(fence.HTML, want) {
			t.Errorf("fence markup is missing %s:\n%s", want, fence.HTML)
		}
	}
	// Strip the annotation and the diagram must read exactly as it was
	// written — anchors annotate, they never rewrite.
	bare := stripTags(fence.HTML)
	source := diagramDoc[strings.Index(diagramDoc, "flowchart LR"):strings.Index(diagramDoc, "```\n\nNotes")]
	if bare != source {
		t.Errorf("annotated source differs:\n%q\nwant:\n%q", bare, source)
	}
}

// A note anchored to a statement quotes the statement, not the diagram: the
// hash is the statement's own, so editing one edge does not stale the others.
func TestMermaidStatementAnchorsAreIndependent(t *testing.T) {
	before, err := ParseBytes("diagram.md", []byte(diagramDoc))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(diagramDoc, "worker -.retry.-> queue", "worker -.retry.-> dlq", 1)
	after, err := ParseBytes("diagram.md", []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if h := block(t, after, "1/3/api-->queue").Hash; h != block(t, before, "1/3/api-->queue").Hash {
		t.Error("an untouched edge should keep its hash")
	}
	// The edited edge now points somewhere else, so it is a different block —
	// the note written against the old one surfaces as unanchored rather than
	// silently reading as a note about the new target.
	for _, b := range after.Blocks {
		if b.ID == "1/3/worker-->queue" {
			t.Error("the retried edge should no longer anchor to the old target")
		}
	}
	if got := block(t, after, "1/3/worker-->dlq").Quote; !strings.Contains(got, "dlq") {
		t.Errorf("new edge quote = %q", got)
	}
}

// A .mmd file is a document in its own right, anchored the same way.
func TestMermaidDocument(t *testing.T) {
	doc, err := ParseBytes("flow.mmd", []byte("flowchart TD\n  a[Start] --> b{Choice}\n  b -->|yes| c[Ship]\n  subgraph fix [Fixing]\n    b -->|no| d[Fix]\n  end\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != FormatMermaid || !doc.IsTree() {
		t.Fatalf("format = %q, tree = %v", doc.Format, doc.IsTree())
	}
	want := []string{"flowchart", "a-->b", "b-->c", "fix", "fix/b-->d"}
	if got := ids(doc); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("blocks = %v, want %v", got, want)
	}
	sub := block(t, doc, "fix")
	if !sub.HasChildren || sub.Level != 0 {
		t.Errorf("subgraph = %+v", sub)
	}
	if kid := block(t, doc, "fix/b-->d"); kid.Parent != "fix" || kid.Level != 1 {
		t.Errorf("subgraph member = %+v", kid)
	}
	if got := block(t, doc, "a-->b").HTML; !strings.Contains(got, "a[Start] --&gt; b{Choice}") {
		t.Errorf("statement html = %q", got)
	}
}

// A diagram type Marginalia does not take apart keeps every character and
// stays reviewable as the source block it already was.
func TestMermaidUnsupportedDiagramKeepsSource(t *testing.T) {
	src := "sequenceDiagram\n  Alice->>Bob: Hi\n  Bob-->>Alice: Hello\n"
	doc, err := ParseBytes("seq.mmd", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 {
		t.Fatalf("blocks = %v", ids(doc))
	}
	one := doc.Blocks[0]
	if one.Kind != mermaidSourceKind {
		t.Errorf("kind = %q", one.Kind)
	}
	for _, want := range []string{"Alice-&gt;&gt;Bob: Hi", "Bob--&gt;&gt;Alice: Hello"} {
		if !strings.Contains(one.HTML, want) {
			t.Errorf("source block is missing %q:\n%s", want, one.HTML)
		}
	}
}

// The same fallback inside markdown: the fence is the code block it is today,
// with the anchor it already had, and nothing extra.
func TestMermaidUnsupportedFenceStaysOneBlock(t *testing.T) {
	doc, err := ParseBytes("doc.md", []byte("# T\n\n```mermaid\nsequenceDiagram\n  Alice->>Bob: Hi\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(doc); strings.Join(got, ",") != "1/1,1/2" {
		t.Fatalf("blocks = %v", got)
	}
	fence := block(t, doc, "1/2")
	if fence.HasChildren {
		t.Error("an unparsed diagram has no statements to anchor")
	}
	if !strings.Contains(fence.HTML, "Alice-&gt;&gt;Bob: Hi") {
		t.Errorf("fence lost its source: %s", fence.HTML)
	}
}

// Two identical statements are both legal and both reviewable, so their
// anchors have to differ.
func TestMermaidRepeatedStatementsGetDistinctAnchors(t *testing.T) {
	doc, err := ParseBytes("flow.mmd", []byte("flowchart LR\n  a --> b\n  a --> b\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(doc); strings.Join(got, ",") != "flowchart,a-->b,a-->b#2" {
		t.Errorf("blocks = %v", got)
	}
}

// A code fence in another language is still an ordinary code block.
func TestNonMermaidFenceUnchanged(t *testing.T) {
	doc, err := ParseBytes("doc.md", []byte("```go\nfunc main() {}\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 || doc.Blocks[0].HasChildren {
		t.Fatalf("blocks = %+v", doc.Blocks)
	}
	if !strings.Contains(doc.Blocks[0].HTML, `class="language-go"`) {
		t.Errorf("html = %s", doc.Blocks[0].HTML)
	}
}

func ids(doc *Document) []string {
	out := make([]string, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		out = append(out, b.ID)
	}
	return out
}

func block(t *testing.T, doc *Document, id string) Block {
	t.Helper()
	for _, b := range doc.Blocks {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("no block %q in %v", id, ids(doc))
	return Block{}
}

// stripTags removes markup, leaving the text a reader sees — enough to prove
// the source came through the annotation intact.
func stripTags(html string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(html); i++ {
		switch c := html[i]; {
		case c == '<':
			depth++
		case c == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteByte(c)
		}
	}
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&amp;", "&").Replace(b.String())
}
