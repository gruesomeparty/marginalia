package document

import "testing"

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
