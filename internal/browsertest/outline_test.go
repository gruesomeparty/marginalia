package browsertest

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/session"
)

const chapters = "# One\n\nFirst chapter.\n\n# Two\n\nSecond chapter.\n\nMore of it.\n"

// The outline counts per section with the header's own rule, and a section
// ticks once every block in it is answered (issue #132).
func TestOutlineTicksAFinishedSection(t *testing.T) {
	r := serve(t, "book.md", chapters, session.Options{})
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(chromedp.CSS(".outline")),
	)
	rows := get(t, ctx, chromedp.Evaluate[[]string](`Array.from(document.querySelectorAll(".outline .sec")).map(function(b){return b.textContent})`))
	if strings.Join(rows, "|") != "One|Two" {
		t.Fatalf("outline rows = %q, want One, Two", rows)
	}

	// Answer both blocks of the first chapter, the heading and its paragraph.
	approve := chromedp.CSS(`.composer .chip[data-type="approve"]`)
	for i, id := range []string{r.block(t, "heading", "One"), r.block(t, "paragraph", "First")} {
		do(t, ctx,
			chromedp.Click(sel(id)),
			chromedp.WaitVisible(approve),
			chromedp.Click(approve),
			chromedp.WaitNotPresent(chromedp.CSS(".composer")),
		)
		r.events(t, i+1)
	}
	ticked := get(t, ctx, chromedp.Evaluate[[]bool](`Array.from(document.querySelectorAll(".outline .tick")).map(function(t){return !t.hidden})`))
	if len(ticked) != 2 || !ticked[0] || ticked[1] {
		t.Errorf("ticks = %v, want only the first chapter's", ticked)
	}
	head := get(t, ctx, chromedp.Text(chromedp.CSS("#progtext")))
	if !strings.Contains(head, "1 of 2 sections") {
		t.Errorf("header says %q, want it to count 1 of 2 sections", head)
	}
	left := get(t, ctx, chromedp.Text(chromedp.CSS(`.outline li[data-section] + li .of`)))
	if left != "0/3" {
		t.Errorf("second chapter reads %q, want 0/3", left)
	}
}
