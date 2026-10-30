package document

import (
	"strings"
	"testing"
)

const migrationSQL = `-- add the lookup the dashboard needs
CREATE INDEX orders_customer_idx ON orders (customer_id);

ALTER TABLE orders ADD COLUMN note text;

INSERT INTO orders (note) VALUES ('a; b');
`

// Issue #152: one block per statement, anchored by ordinal, kind and object.
func TestSQLFileAnchorsPerStatement(t *testing.T) {
	doc, err := ParseBytes("0042_add_index.sql", []byte(migrationSQL))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != FormatSQL || !doc.IsTree() {
		t.Fatalf("format %q, tree %v", doc.Format, doc.IsTree())
	}
	want := []string{"1:create-index:orders_customer_idx", "2:alter-table:orders", "3:insert:orders"}
	if got := ids(doc); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	first := doc.Blocks[0]
	if first.Kind != "create-index" || !strings.HasPrefix(first.Quote, "add the lookup") {
		t.Errorf("first block = %q quoting %q", first.Kind, first.Quote)
	}
	if !strings.Contains(first.HTML, `class="cmt"`) || !strings.Contains(first.HTML, `class="tok kw"`) {
		t.Errorf("statement not rendered with its comment and highlighted: %s", first.HTML)
	}
}

// Inserting a statement moves the ordinals after it: an old note's id names
// a different statement, or none, and never silently lands on the newcomer.
// When the newcomer shares kind and object, the hash marks the note stale.
func TestSQLInsertedStatementNeverMisanchors(t *testing.T) {
	before, _ := ParseBytes("m.sql", []byte("ALTER TABLE orders ADD a int;\nALTER TABLE orders ADD b int;\n"))
	after, _ := ParseBytes("m.sql", []byte("ALTER TABLE orders ADD z int;\nALTER TABLE orders ADD a int;\nALTER TABLE orders ADD b int;\n"))
	old := before.Blocks[0]
	for _, b := range after.Blocks {
		if b.ID == old.ID && b.Hash == old.Hash {
			t.Fatalf("note on %q would anchor to the inserted statement %q unflagged", old.Quote, b.Quote)
		}
	}
	if after.Blocks[1].Hash != old.Hash || after.Blocks[1].ID == old.ID {
		t.Errorf("moved statement: id %q hash %q, was %q %q", after.Blocks[1].ID, after.Blocks[1].Hash, old.ID, old.Hash)
	}
}

// A comment trailing a statement is shown with it and is part of its hash.
func TestSQLTrailingComment(t *testing.T) {
	a, _ := ParseBytes("m.sql", []byte("DROP TABLE x; -- after backfill\nSELECT 1;\n"))
	b, _ := ParseBytes("m.sql", []byte("DROP TABLE x; -- before backfill\nSELECT 1;\n"))
	if !strings.Contains(a.Blocks[0].HTML, "after backfill") || a.Blocks[0].Hash == b.Blocks[0].Hash {
		t.Errorf("trailing comment not rendered or not hashed: %s", a.Blocks[0].HTML)
	}
	if a.Blocks[1].Hash != b.Blocks[1].Hash {
		t.Error("the next statement's hash moved with the previous line's comment")
	}
}

func TestSQLEmptyAndMalformedStillRender(t *testing.T) {
	doc, err := ParseBytes("empty.sql", []byte("\n\n"))
	if err != nil || len(doc.Blocks) != 1 || doc.Blocks[0].ID != "@empty" {
		t.Fatalf("empty file: %v %v", err, doc)
	}
	doc, err = ParseBytes("broken.sql", []byte("SELECT 'never closed; FROM t;\nDROP TABLE x"))
	if err != nil || len(doc.Blocks) == 0 {
		t.Fatalf("malformed file: %v %v", err, doc)
	}
}

const sqlDoc = "# Migration\n\n```sql\n" +
	"-- the slow one\nALTER TABLE orders ADD COLUMN note text;\nCREATE INDEX ON orders (note);\n" +
	"```\n\n```sql\nSELECT 1;\n```\n"

// A ```sql fence anchors per statement inline, under the fence's own id; a
// single-statement fence stays the one block it was.
func TestSQLFenceStatementsAreInline(t *testing.T) {
	doc, err := ParseBytes("doc.md", []byte(sqlDoc))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1/1", "1/2", "1/2/1:alter-table:orders", "1/2/2:create-index:orders", "1/3"}
	if got := ids(doc); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	fence := doc.Blocks[1]
	if !fence.HasChildren || !strings.Contains(fence.HTML, `data-block="1/2/1:alter-table:orders"`) {
		t.Errorf("fence does not carry its statements: %s", fence.HTML)
	}
	if !strings.Contains(fence.HTML, `<span class="tok com">-- the slow one</span>`) {
		t.Errorf("fence lost its highlighting: %s", fence.HTML)
	}
	stmt := doc.Blocks[2]
	if !stmt.Inline || stmt.Parent != "1/2" || stmt.Level != 1 || !strings.HasPrefix(stmt.Quote, "the slow one") {
		t.Errorf("statement block = %+v", stmt)
	}
	if doc.Blocks[4].HasChildren || !strings.Contains(doc.Blocks[4].HTML, "language-sql") {
		t.Errorf("single-statement fence = %+v", doc.Blocks[4])
	}
}
