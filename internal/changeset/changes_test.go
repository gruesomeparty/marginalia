package changeset

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
)

func parse(t *testing.T, src string) *document.Document {
	t.Helper()
	d, err := document.ParseBytes("doc.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBetween(t *testing.T) {
	old := parse(t, "# Plan\n\nShip on Friday.\n\nKeep the cache.\n\nDrop the old flag.\n")
	now := parse(t, "# Plan\n\nShip on Monday.\n\nKeep the cache.\n\nA new paragraph.\n")
	got := Between(old, now)
	var kinds []string
	for _, c := range got {
		kinds = append(kinds, c.Kind+" "+c.Quote)
	}
	// Blocks are ids by position, so the third paragraph is an edit, not a
	// removal plus an addition — the same reading notes get.
	want := []string{"edited Ship on Monday.", "edited A new paragraph."}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("changes %q, want %q", kinds, want)
	}
	if got[0].Redline == nil {
		t.Fatal("an edited block carries no redline")
	}
}

func TestRemovedAndAdded(t *testing.T) {
	old := parse(t, "# Plan\n\nOne.\n\nTwo.\n\n# Later\n\nThree.\n")
	now := parse(t, "# Plan\n\nOne.\n\n# Later\n\nThree.\n\nFour.\n")
	var got []string
	for _, c := range Between(old, now) {
		got = append(got, c.Kind+" "+c.Block)
	}
	added, edited, removed := IDs(Between(old, now))
	if len(added) != 1 || len(removed) != 1 || len(edited) != 0 {
		t.Fatalf("changes %q", got)
	}
	if got[len(got)-1] != "removed "+removed[0] {
		t.Errorf("removed blocks come last: %q", got)
	}
}

func TestNoBaselineNoChanges(t *testing.T) {
	if got := Between(nil, parse(t, "# A\n")); got != nil {
		t.Errorf("no baseline: %v", got)
	}
	d := parse(t, "# A\n\nText.\n")
	if got := Between(d, d); got != nil {
		t.Errorf("same parse: %v", got)
	}
}

// An edited list item is reported once, as the item; the list it sits in
// is explained by it.
func TestListItemIsReportedNotItsList(t *testing.T) {
	old := parse(t, "# L\n\n- alpha\n- beta\n")
	now := parse(t, "# L\n\n- alpha\n- gamma\n")
	got := Between(old, now)
	if len(got) != 1 || got[0].Kind != Edited || !strings.Contains(got[0].Quote, "gamma") {
		t.Fatalf("changes %+v, want the one item", got)
	}
}

func TestWords(t *testing.T) {
	spans := Words("Ship on Friday after review.", "Ship on Monday after the review.")
	var oldText, newText strings.Builder
	for _, s := range spans {
		if s.Op != "+" {
			oldText.WriteString(s.Text)
		}
		if s.Op != "-" {
			newText.WriteString(s.Text)
		}
	}
	if oldText.String() != "Ship on Friday after review." || newText.String() != "Ship on Monday after the review." {
		t.Fatalf("spans do not rebuild both texts: %+v", spans)
	}
	want := []Span{{"=", "Ship on "}, {"-", "Friday "}, {"+", "Monday "}, {"=", "after "}, {"+", "the "}, {"=", "review."}}
	if !reflect.DeepEqual(spans, want) {
		t.Errorf("spans %+v\nwant %+v", spans, want)
	}
}

func TestWordsTooLargeIsAReplacement(t *testing.T) {
	a := strings.Repeat("a ", 2000) + "x"
	b := strings.Repeat("b ", 2000) + "x"
	spans := Words(a, b)
	if len(spans) != 3 || spans[0].Op != "-" || spans[1].Op != "+" || spans[2].Text != "x" {
		t.Fatalf("got %d spans", len(spans))
	}
}

func TestWithoutSkipsTheRedline(t *testing.T) {
	old, now := parse(t, "# T\n\nOne two three.\n"), parse(t, "# T\n\nOne four three.\n")
	with, without := Between(old, now), Without(old, now)
	if len(with) != 1 || len(without) != 1 || with[0].Block != without[0].Block {
		t.Fatalf("Between %+v, Without %+v", with, without)
	}
	if len(with[0].Redline) == 0 || without[0].Redline != nil {
		t.Errorf("redlines: Between %v, Without %v", with[0].Redline, without[0].Redline)
	}
}
