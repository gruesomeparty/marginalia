package document

import (
	"strings"
	"testing"
)

const listDoc = `# Tasks

- **P0.1 — Repo skeleton.** Create the tree.
- **P0.2 — Tiles.** Render the dashboard.
  - Cache the aggregate query.
  - Ship the empty state first.
- **P0.3 — Catalog.** Import nightly.
`

// The point of issue #12: a note about one bullet lands on that bullet, not on
// the list that happens to contain it.
func TestListItemsAreTheirOwnBlocks(t *testing.T) {
	doc, err := ParseBytes("tasks.md", []byte(listDoc))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id, kind, parent string
		level            int
		inline, kids     bool
	}{
		{"1/1", "heading", "", 1, false, false},
		{"1/2", "list", "", 0, false, true},
		{"1/2.1", listItemKind, "1/2", 1, true, false},
		{"1/2.2", listItemKind, "1/2", 1, true, true},
		{"1/2.2.1", listItemKind, "1/2.2", 2, true, false},
		{"1/2.2.2", listItemKind, "1/2.2", 2, true, false},
		{"1/2.3", listItemKind, "1/2", 1, true, false},
	}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d: %+v", len(doc.Blocks), len(want), doc.Blocks)
	}
	for i, w := range want {
		got := doc.Blocks[i]
		if got.ID != w.id || got.Kind != w.kind || got.Parent != w.parent ||
			got.Level != w.level || got.Inline != w.inline || got.HasChildren != w.kids {
			t.Errorf("block %d = {%s %s parent=%s level=%d inline=%v kids=%v}, want {%s %s parent=%s level=%d inline=%v kids=%v}",
				i, got.ID, got.Kind, got.Parent, got.Level, got.Inline, got.HasChildren,
				w.id, w.kind, w.parent, w.level, w.inline, w.kids)
		}
	}
}

// An item's anchor covers what the item itself says: a sub-bullet's text is
// its own business, so editing one does not stale the bullet above it.
func TestListItemAnchorsAreIndependent(t *testing.T) {
	before, err := ParseBytes("tasks.md", []byte(listDoc))
	if err != nil {
		t.Fatal(err)
	}
	parent := blockByID(before, "1/2.2")
	if parent.Quote != "P0.2 — Tiles. Render the dashboard." {
		t.Errorf("quote = %q, want the item's own text without its sub-items", parent.Quote)
	}
	after, err := ParseBytes("tasks.md", []byte(strings.Replace(listDoc, "Cache the aggregate query.", "Cache the rollup query.", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if blockByID(after, "1/2.2").Hash != parent.Hash {
		t.Error("editing a sub-item moved its parent item's anchor")
	}
	if blockByID(after, "1/2.1").Hash != blockByID(before, "1/2.1").Hash {
		t.Error("editing one item moved a sibling's anchor")
	}
	if blockByID(after, "1/2.2.1").Hash == blockByID(before, "1/2.2.1").Hash {
		t.Error("the edited item's own hash should go stale")
	}
}

// The list keeps the ID it always had, so a note about the shape of the list
// still has somewhere to live — and feedback written before item anchoring
// existed still anchors.
func TestListBlockSurvivesItemAnchoring(t *testing.T) {
	doc, err := ParseBytes("tasks.md", []byte(listDoc))
	if err != nil {
		t.Fatal(err)
	}
	list := blockByID(doc, "1/2")
	if list == nil || list.Inline {
		t.Fatalf("list block = %+v", list)
	}
	if !strings.Contains(list.HTML, `<li class="block" data-block="1/2.1"`) {
		t.Errorf("the list's markup should carry its items' anchors: %q", list.HTML)
	}
	for _, want := range []string{`data-hash="`, `data-quote="P0.1`, `data-kind="list_item"`, `role="button"`, "<ul>", "</ul>"} {
		if !strings.Contains(list.HTML, want) {
			t.Errorf("list HTML missing %q", want)
		}
	}
}

func TestOrderedListKeepsItsNumbering(t *testing.T) {
	doc, err := ParseBytes("t.md", []byte("3. third\n4. fourth\n"))
	if err != nil {
		t.Fatal(err)
	}
	html := blockByID(doc, "0/1").HTML
	if !strings.Contains(html, `<ol start="3">`) {
		t.Errorf("ordered list should keep its start: %q", html)
	}
	if blockByID(doc, "0/1.1").Quote != "third" {
		t.Errorf("first item = %+v", blockByID(doc, "0/1.1"))
	}
	plain, err := ParseBytes("t.md", []byte("1. one\n2. two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blockByID(plain, "0/1").HTML, "start=") {
		t.Error("a list starting at 1 needs no start attribute")
	}
}

// A loose list keeps its paragraphs; a tight one stays tight.
func TestListSpacingIsPreserved(t *testing.T) {
	loose, err := ParseBytes("t.md", []byte("- one\n\n- two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(blockByID(loose, "0/1").HTML, "<p>one</p>") {
		t.Errorf("loose item should keep its paragraph: %q", blockByID(loose, "0/1").HTML)
	}
	tight, err := ParseBytes("t.md", []byte("- one\n- two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blockByID(tight, "0/1").HTML, "<p>") {
		t.Errorf("tight item should not gain a paragraph: %q", blockByID(tight, "0/1").HTML)
	}
}

func TestListItemAttributesAreEscaped(t *testing.T) {
	doc, err := ParseBytes("t.md", []byte("- a \"quoted\" <script>alert(1)</script> item\n"))
	if err != nil {
		t.Fatal(err)
	}
	html := blockByID(doc, "0/1").HTML
	if strings.Contains(html, `data-quote="a "quoted"`) {
		t.Errorf("attribute quotes not escaped: %q", html)
	}
	if !strings.Contains(html, "&#34;quoted&#34;") {
		t.Errorf("expected escaped quote in the anchor attributes: %q", html)
	}
	if strings.Contains(html, `data-quote="a &#34;quoted&#34; <script>`) {
		t.Errorf("attribute markup not escaped: %q", html)
	}
}

// A list inside another block (a blockquote, a table cell) belongs to that
// block: it renders as the document wrote it, without item anchors.
func TestListInsideAnotherBlockIsNotSplit(t *testing.T) {
	doc, err := ParseBytes("t.md", []byte("> - quoted item\n> - another\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != "blockquote" {
		t.Fatalf("blocks = %+v, want a single blockquote", doc.Blocks)
	}
	if strings.Contains(doc.Blocks[0].HTML, `class="block"`) {
		t.Errorf("a nested list should not be annotated: %q", doc.Blocks[0].HTML)
	}
}
