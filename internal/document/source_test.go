package document

import (
	"strings"
	"testing"
)

const handlerGo = `package ingest

// Batch collects.
type Batch struct {
	Cap int
}

// Full reports whether the batch is full.
func (b *Batch) Full() bool {
	return b.Cap > 0
}

func Ingest(b *Batch) error {
	return nil
}
`

// Issue #31: a Go file is a tree of its declarations, anchored by symbol
// path, in the order the file declares them.
func TestSourceAnchorsBySymbolPath(t *testing.T) {
	doc, err := ParseBytes("handler.go", []byte(handlerGo))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != FormatSource || !doc.IsTree() || FormatFor("x.go") != FormatSource {
		t.Fatalf("format %q, tree %v", doc.Format, doc.IsTree())
	}
	want := []string{"package", "Batch", "Batch/Cap", "Batch/Full", "Ingest"}
	if got := ids(doc); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	blocks := map[string]Block{}
	for _, b := range doc.Blocks {
		blocks[b.ID] = b
	}
	if b := blocks["Batch/Cap"]; b.Parent != "Batch" || b.Level != 1 || !blocks["Batch"].HasChildren {
		t.Errorf("field under %q at level %d", b.Parent, b.Level)
	}
	if b := blocks["Batch/Full"]; b.Kind != "method" || !strings.HasPrefix(b.Quote, "// Full reports") ||
		!strings.Contains(b.HTML, `<span class="tok kw">return</span>`) {
		t.Errorf("method = %q quoting %q: %s", b.Kind, b.Quote, b.HTML)
	}
	if b := blocks["Batch"]; strings.Contains(b.HTML, "Cap") || !strings.HasSuffix(b.HTML, "struct</span> {</code></span>") {
		t.Errorf("a struct renders its header, its fields are their own blocks: %s", b.HTML)
	}
}

// The acceptance criterion's second half: an edit elsewhere leaves a note's
// hash alone; an edit inside a method changes that method only — not the
// type it belongs to.
func TestSourceEditsElsewhereMoveNothing(t *testing.T) {
	before, _ := ParseBytes("handler.go", []byte(handlerGo))
	edited := strings.Replace(handlerGo, "return nil", "return errFull", 1)
	edited = strings.Replace(edited, "return b.Cap > 0", "return b.Cap >= 0", 1)
	edited = strings.Replace(edited, "package ingest\n", "package ingest\n\nimport \"errors\"\n\nvar errFull = errors.New(\"full\")\n", 1)
	after, _ := ParseBytes("handler.go", []byte(edited))
	now := map[string]string{}
	for _, b := range after.Blocks {
		now[b.ID] = b.Hash
	}
	for _, b := range before.Blocks {
		changed := now[b.ID] != b.Hash
		if want := b.ID == "Ingest" || b.ID == "Batch/Full"; changed != want {
			t.Errorf("%s: hash changed %v, want %v", b.ID, changed, want)
		}
	}
}

// A file that does not compile still renders, and an empty one says so.
func TestSourceToleratesBrokenAndEmptyFiles(t *testing.T) {
	doc, err := ParseBytes("broken.go", []byte("package p\n\nfunc F( {\n\n}}} type\n"))
	if err != nil || len(doc.Blocks) < 2 || doc.Blocks[0].ID != "package" {
		t.Fatalf("broken file: %v %v", err, ids(doc))
	}
	empty, err := ParseBytes("empty.go", nil)
	if err != nil || len(empty.Blocks) != 1 || empty.Blocks[0].ID != "@empty" {
		t.Fatalf("empty file: %v %v", err, ids(empty))
	}
}
