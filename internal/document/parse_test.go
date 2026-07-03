package document

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTable(t *testing.T) {
	src := []byte("| a | b |\n|---|---|\n| 1 | 2 |\n")
	doc, err := ParseBytes("t.md", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != "table" {
		t.Fatalf("got %+v, want single table block", doc.Blocks)
	}
	if doc.Blocks[0].HTML == "" {
		t.Fatal("table HTML empty")
	}
}

func TestParseReadsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(p, []byte("# H\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(p)
	if err != nil || len(doc.Blocks) != 2 {
		t.Fatalf("Parse: %v blocks=%d", err, len(doc.Blocks))
	}
	if _, err := Parse(filepath.Join(t.TempDir(), "nope.md")); err == nil {
		t.Error("expected error for missing file")
	}
}
