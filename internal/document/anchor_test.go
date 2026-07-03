package document

import "testing"

func TestSectionPathsAndOrdinals(t *testing.T) {
	src := []byte("intro para\n\n# First\n\npara under first\n\n## Nested\n\ndeep para\n")
	doc, err := ParseBytes("t.md", src)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ id, kind string }{
		{"0/1", "paragraph"},
		{"1/1", "heading"},
		{"1/2", "paragraph"},
		{"1.1/1", "heading"},
		{"1.1/2", "paragraph"},
	}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d: %+v", len(doc.Blocks), len(want), doc.Blocks)
	}
	for i, w := range want {
		if doc.Blocks[i].ID != w.id || doc.Blocks[i].Kind != w.kind {
			t.Errorf("block %d = %s/%s, want %s/%s", i, doc.Blocks[i].ID, doc.Blocks[i].Kind, w.id, w.kind)
		}
	}
}

func TestQuoteAndHashStable(t *testing.T) {
	a, _ := ParseBytes("t.md", []byte("# Same text here\n"))
	b, _ := ParseBytes("other.md", []byte("#    Same   text here   \n"))
	if a.Blocks[0].Hash != b.Blocks[0].Hash {
		t.Errorf("hash not whitespace-stable: %s vs %s", a.Blocks[0].Hash, b.Blocks[0].Hash)
	}
	if len(a.Blocks[0].Hash) != 12 {
		t.Errorf("hash len = %d, want 12", len(a.Blocks[0].Hash))
	}
}

func TestQuoteTruncation(t *testing.T) {
	long := "word " // build > 90 runes
	for i := 0; i < 40; i++ {
		long += "word "
	}
	doc, _ := ParseBytes("t.md", []byte(long))
	q := doc.Blocks[0].Quote
	if []rune(q)[len([]rune(q))-1] != '…' {
		t.Errorf("expected ellipsis, got %q", q)
	}
	if len([]rune(q)) > 91 {
		t.Errorf("quote too long: %d runes", len([]rune(q)))
	}
}

func TestCodeBlockText(t *testing.T) {
	doc, _ := ParseBytes("t.md", []byte("```\nfmt.Println(\"hi\")\n```\n"))
	if doc.Blocks[0].Kind != "code" {
		t.Fatalf("kind = %s, want code", doc.Blocks[0].Kind)
	}
	if doc.Blocks[0].Hash == "" {
		t.Fatal("code block hash empty")
	}
}

func TestNoHeadings(t *testing.T) {
	doc, _ := ParseBytes("t.md", []byte("just one paragraph\n"))
	if doc.Blocks[0].ID != "0/1" {
		t.Errorf("id = %s, want 0/1", doc.Blocks[0].ID)
	}
}
