package mermaid

import (
	"errors"
	"strings"
	"testing"
)

// flat returns every statement of a diagram in source order as
// "kind name" lines, which is what the anchors are derived from.
func flat(stmts []*Stmt) []string {
	var out []string
	for _, s := range stmts {
		out = append(out, s.Kind+" "+s.Name)
		for _, kid := range flat(s.Children) {
			out = append(out, "  "+kid)
		}
	}
	return out
}

func parse(t *testing.T, src string) *Diagram {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func TestParseFlowchart(t *testing.T) {
	d := parse(t, `flowchart LR
  %% the public entry point
  client[Client] --> api[Ingest API]
  api --> queue[(Queue)]
  queue --> worker{{Worker}}
  worker -.retry.-> queue
  worker -- ok --> store[(Store)]
  api ==>|fast| cache
  a --> b --> c
  standalone[On its own]
  subgraph payments [Payments]
    direction TB
    charge --> refund
  end
  classDef default fill:#f9f
  click api "https://example.com" "docs"
`)
	if d.Type != "flowchart" {
		t.Errorf("Type = %q", d.Type)
	}
	want := []string{
		"diagram flowchart",
		"edge client-->api",
		"edge api-->queue",
		"edge queue-->worker",
		// The dotted, labelled edge from issue #25: label and line style are
		// not part of what the edge connects.
		"edge worker-->queue",
		"edge worker-->store",
		"edge api-->cache",
		"edge a-->b-->c",
		"node standalone",
		"subgraph payments",
		"  direction direction/TB",
		"  edge charge-->refund",
		"class_def class_def/default",
		"click click/api",
	}
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

// A comment belongs to the statement below it, the way a proto comment
// belongs to its declaration.
func TestParseAttachesComments(t *testing.T) {
	d := parse(t, "graph TD\n  %% why this hop exists\n  %% and who owns it\n  a --> b\n")
	edge := d.Stmts[1]
	if edge.Comment != "why this hop exists and who owns it" {
		t.Errorf("Comment = %q", edge.Comment)
	}
	if edge.Text != "a --> b" {
		t.Errorf("Text = %q", edge.Text)
	}
}

// Nothing the author wrote may vanish: a comment with no statement under it
// becomes a statement of its own rather than being dropped.
func TestParseKeepsStrandedComments(t *testing.T) {
	d := parse(t, "flowchart LR\n  subgraph s\n    a --> b\n    %% nothing else yet\n  end\n  %% the end\n")
	sub := d.Stmts[1]
	if len(sub.Children) != 2 || sub.Children[1].Kind != KindComment {
		t.Fatalf("subgraph children = %v", flat(sub.Children))
	}
	last := d.Stmts[len(d.Stmts)-1]
	if last.Kind != KindComment || last.Text != "%% the end" {
		t.Errorf("trailing comment = %+v", last)
	}
}

func TestParseRanges(t *testing.T) {
	src := "flowchart LR\n  %% note\n  a --> b\n"
	d := parse(t, src)
	for _, s := range d.Stmts {
		if s.Start < 0 || s.End > len(src) || s.End <= s.Start {
			t.Fatalf("bad range on %s: [%d,%d)", s.Name, s.Start, s.End)
		}
	}
	// The range covers the comment through the statement, and nothing of the
	// indentation in front of them.
	if got := src[d.Stmts[1].Start:d.Stmts[1].End]; got != "%% note\n  a --> b" {
		t.Errorf("edge range covers %q", got)
	}
	if got := src[d.Stmts[0].Start:d.Stmts[0].End]; got != "flowchart LR" {
		t.Errorf("header range covers %q", got)
	}
}

// The parser reads the diagram, not the prose in it: an arrow inside a node
// label is part of the label.
func TestParseIgnoresArrowsInLabels(t *testing.T) {
	d := parse(t, `flowchart LR
  A["a --> b, in words"] --> B
  C[/"x;y"/] --> D
`)
	want := []string{"diagram flowchart", "edge A-->B", "edge C-->D"}
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s", got)
	}
}

// Semicolons separate statements, including on the header's own line.
func TestParseSemicolons(t *testing.T) {
	d := parse(t, "graph TD; a-->b; b-->c\n")
	want := []string{"diagram graph", "edge a-->b", "edge b-->c"}
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s", got)
	}
}

// `o` and `x` are edge heads only when a space sets them off — otherwise
// `foo-->bar` would lose the o of foo, as it does in mermaid itself.
func TestParseCircleAndCrossHeads(t *testing.T) {
	d := parse(t, "flowchart LR\n  foo-->bar\n  n1 o--o n2\n  n3 x--x n4\n  n5 ~~~ n6\n  box---orders\n")
	want := []string{
		"diagram flowchart",
		"edge foo-->bar",
		"edge n1-->n2",
		"edge n3-->n4",
		"edge n5-->n6",
		"edge box-->orders",
	}
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s", got)
	}
}

// A label written between two halves of a link is a label, not a node in a
// chain: the opening half has no arrow head, which is how mermaid tells them
// apart and how we do too.
func TestParseLabelsBetweenLinkHalves(t *testing.T) {
	d := parse(t, "flowchart LR\n  a -- plain --> b\n  c -. dotted .-> d\n  e == thick ==> f\n  g -- \"quoted\" --- h\n")
	want := []string{
		"diagram flowchart",
		"edge a-->b",
		"edge c-->d",
		"edge e-->f",
		"edge g-->h",
	}
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s", got)
	}
}

func TestParsePrelude(t *testing.T) {
	d := parse(t, "---\ntitle: Ingest\n---\n%%{init: {\"theme\":\"neutral\"}}%%\nflowchart LR\n  a --> b\n")
	want := []string{"frontmatter frontmatter", "init init", "diagram flowchart", "edge a-->b"}
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s", got)
	}
}

// Anything that is not a flowchart is not an error — the caller keeps it as
// the source block it already had.
func TestParseUnsupported(t *testing.T) {
	for _, src := range []string{
		"sequenceDiagram\n  Alice->>Bob: Hi\n",
		"classDiagram\n  Animal <|-- Duck\n",
		"",
		"%% just a comment\n",
		"---\ntitle: no diagram\n---\n",
	} {
		if _, err := Parse([]byte(src)); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) error = %v, want ErrUnsupported", src, err)
		}
	}
}

// An unrecognized statement keeps its line rather than being dropped, and an
// `end` with nothing open does not unbalance the tree.
func TestParseKeepsUnknownStatements(t *testing.T) {
	d := parse(t, "flowchart LR\n  end\n  ?!(what)\n  a --> b\n")
	want := []string{"diagram flowchart", "statement end", "node ?!", "edge a-->b"}
	if got := strings.Join(flat(d.Stmts), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("statements:\n%s", got)
	}
}

// An unclosed subgraph keeps its children rather than losing them.
func TestParseUnclosedSubgraph(t *testing.T) {
	d := parse(t, "flowchart LR\n  subgraph s\n    a --> b\n")
	if len(d.Stmts) != 2 || len(d.Stmts[1].Children) != 1 {
		t.Fatalf("statements: %v", flat(d.Stmts))
	}
}
