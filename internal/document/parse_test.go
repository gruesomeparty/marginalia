package document

import (
	"os"
	"path/filepath"
	"strings"
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

// A resolver rewrites the destinations it claims, in the HTML only: the
// block's text and hash are what anchors, and they must not move.
func TestLinkResolverRewritesOnlyTheHTML(t *testing.T) {
	src := []byte("See ![mock](mock.png) and [the spec](./mock.png) and ![x](https://example.com/x.png).\n")
	plain, err := ParseBytes("plan.md", src)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseBytesWith("plan.md", src, Options{Link: func(dest string) (string, bool) {
		if strings.TrimPrefix(dest, "./") == "mock.png" {
			return "/r/x/assets/1/mock.png", true
		}
		return "", false
	}})
	if err != nil {
		t.Fatal(err)
	}
	html := doc.Blocks[0].HTML
	if strings.Count(html, "/r/x/assets/1/mock.png") != 2 || !strings.Contains(html, "https://example.com/x.png") {
		t.Errorf("html = %s", html)
	}
	if doc.Blocks[0].Hash != plain.Blocks[0].Hash || doc.Blocks[0].PlainText != plain.Blocks[0].PlainText {
		t.Error("resolving a link changed what the block anchors on")
	}
}
