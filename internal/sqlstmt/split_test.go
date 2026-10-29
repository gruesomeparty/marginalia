package sqlstmt

import (
	"strings"
	"testing"
)

const migration = `-- 0042: index orders by customer
BEGIN;

-- the lookup the dashboard does
CREATE INDEX CONCURRENTLY IF NOT EXISTS orders_customer_idx ON orders (customer_id);

ALTER TABLE ONLY "public"."orders"
  ADD COLUMN note text DEFAULT 'it''s; fine';

/* a function body holds semicolons of its own */
CREATE OR REPLACE FUNCTION touch() RETURNS trigger AS $fn$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$fn$ LANGUAGE plpgsql;

CREATE TRIGGER orders_touch BEFORE UPDATE ON orders
FOR EACH ROW BEGIN
  UPDATE orders SET seen = CASE WHEN 1 = 1 THEN 1 ELSE 0 END;
  IF 1 THEN SELECT 1; END IF;
END;

INSERT INTO orders (note) VALUES ($1), ('C:\'), (E'a\';b');
COMMIT;
-- trailing thought
`

func TestSplitMigration(t *testing.T) {
	got := Split([]byte(migration))
	want := []struct{ kind, object, comment string }{
		{"begin", "", "0042: index orders by customer"},
		{"create-index", "orders_customer_idx", "the lookup the dashboard does"},
		{"alter-table", "public.orders", ""},
		{"create-function", "touch", "a function body holds semicolons of its own"},
		{"create-trigger", "orders_touch", ""},
		{"insert", "orders", ""},
		{"commit", "", ""},
		{KindComment, "", ""},
	}
	if len(got) != len(want) {
		for _, s := range got {
			t.Logf("%s %q: %q", s.Kind, s.Object, s.Text)
		}
		t.Fatalf("got %d statements, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind || g.Object != w.object || g.Comment != w.comment {
			t.Errorf("statement %d = (%q, %q, %q), want (%q, %q, %q)", i+1, g.Kind, g.Object, g.Comment, w.kind, w.object, w.comment)
		}
	}
	if !strings.HasSuffix(got[2].Text, "'it''s; fine';") {
		t.Errorf("a semicolon inside a string ended the statement: %q", got[2].Text)
	}
	if !strings.HasSuffix(got[3].Text, "LANGUAGE plpgsql;") {
		t.Errorf("the dollar-quoted body was split: %q", got[3].Text)
	}
	if !strings.HasSuffix(got[4].Text, "END IF;\nEND;") {
		t.Errorf("the BEGIN … END body was split: %q", got[4].Text)
	}
	if got[7].Text != "-- trailing thought" {
		t.Errorf("trailing comment = %q", got[7].Text)
	}
}

// Ranges cover each statement's comments and its semicolon, in order and
// without overlap, so the source can be annotated in place.
func TestSplitRanges(t *testing.T) {
	stmts := Split([]byte(migration))
	at := 0
	for _, s := range stmts {
		if s.Start < at || s.End <= s.Start || s.End > len(migration) {
			t.Fatalf("%s: range [%d,%d) after %d", s.Kind, s.Start, s.End, at)
		}
		if gap := strings.TrimSpace(migration[at:s.Start]); gap != "" {
			t.Errorf("%s: text between statements left out: %q", s.Kind, gap)
		}
		at = s.End
	}
	if stmts[1].Line != 5 {
		t.Errorf("create-index starts on line %d, want 5", stmts[1].Line)
	}
	if !strings.HasPrefix(migration[stmts[1].Start:], "-- the lookup") {
		t.Errorf("range does not start at the comment above the statement")
	}
}

// END CASE closes the CASE it names, and BEGIN TRAN opens a transaction:
// either one misread would fold the rest of the file into one statement.
func TestSplitBlockEdges(t *testing.T) {
	for name, src := range map[string]string{
		"end case": "CREATE PROCEDURE p() BEGIN\n  CASE x WHEN 1 THEN SELECT 1; END CASE;\nEND;\nSELECT 2;\nSELECT 3;",
		"tsql":     "BEGIN TRAN;\nUPDATE t SET a = 1;\nCOMMIT TRAN;",
	} {
		t.Run(name, func(t *testing.T) {
			if got := Split([]byte(src)); len(got) != 3 {
				t.Errorf("got %d statements, want 3: %+v", len(got), got)
			}
		})
	}
}

// A comment trailing a statement on its line belongs to that statement.
func TestSplitTrailingComment(t *testing.T) {
	src := "SELECT 1; -- one\n-- about two\nSELECT 2;"
	got := Split([]byte(src))
	if len(got) != 2 || got[1].Comment != "about two" || got[0].Trailing != "one" || src[got[0].Start:got[0].End] != "SELECT 1; -- one" {
		t.Errorf("got %+v", got)
	}
}

func TestSplitTolerates(t *testing.T) {
	for name, src := range map[string]string{
		"empty":                "",
		"no semicolon":         "SELECT 1",
		"unterminated string":  "SELECT 'oops; FROM t;",
		"unterminated comment": "SELECT 1; /* never closed",
		"unterminated dollar":  "DO $$ BEGIN",
		"stray END":            "END; END; SELECT 1;",
		"garbage":              ";;; )( 'x",
	} {
		t.Run(name, func(t *testing.T) {
			stmts := Split([]byte(src))
			var b strings.Builder
			for _, s := range stmts {
				b.WriteString(src[s.Start:s.End])
			}
			if strings.Join(strings.Fields(strings.ReplaceAll(b.String(), ";", " ")), "") != strings.Join(strings.Fields(strings.ReplaceAll(src, ";", " ")), "") {
				t.Errorf("statements %q do not cover %q", b.String(), src)
			}
		})
	}
	if got := Split([]byte("SELECT 1")); len(got) != 1 || got[0].Text != "SELECT 1" || got[0].Kind != "select" {
		t.Errorf("unterminated statement = %+v", got)
	}
}

func TestClassify(t *testing.T) {
	for text, want := range map[string][2]string{
		"CREATE UNIQUE INDEX ON orders (id);":          {"create-index", "orders"},
		"create materialized view totals as select 1;": {"create-materialized-view", "totals"},
		"DROP TABLE IF EXISTS `legacy`;":               {"drop-table", "legacy"},
		"DELETE FROM ONLY orders WHERE id = 1;":        {"delete", "orders"},
		"UPDATE orders SET a = 1;":                     {"update", "orders"},
		"TRUNCATE TABLE audit;":                        {"truncate", "audit"},
		"COMMENT ON COLUMN orders.note IS 'x';":        {"comment-on-column", "orders.note"},
		"WITH x AS (SELECT 1) SELECT * FROM x;":        {"with", ""},
		"GRANT SELECT ON orders TO reader;":            {"grant", ""},
		"/* lead */ SELECT 1;":                         {"select", ""},
		`ALTER TABLE "we:ird/name" ADD COLUMN x int;`:  {"alter-table", "weirdname"},
	} {
		kind, object := classify(text)
		if kind != want[0] || object != want[1] {
			t.Errorf("classify(%q) = (%q, %q), want (%q, %q)", text, kind, object, want[0], want[1])
		}
	}
}
