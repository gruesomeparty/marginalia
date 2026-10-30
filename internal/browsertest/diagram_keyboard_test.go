package browsertest

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/gruesomeparty/marginalia/internal/session"
)

// A keyboard reaches every anchored shape in a drawn diagram, in the order
// the source declares the statements, and Enter on one writes a note against
// that statement (issue #154). Escape closes the composer and hands focus
// back to the shape that opened it.
func TestDiagramShapesAreReachableByKeyboard(t *testing.T) {
	src, mmdc := standInMMDC(t)
	r := serve(t, "flow.md", "# Flow\n\n```mermaid\n"+string(src)+"```\n",
		session.Options{Diagrams: "auto", MMDC: mmdc})
	ctx := browser(t)
	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(chromedp.CSS(".diagram .pic svg")),
	)

	// The statements the source declares, in order, that the picture draws.
	want := get(t, ctx, chromedp.Evaluate[[]string](`[...document.querySelectorAll(".diagram .src .block[data-block]")]
		.map(s => s.dataset.block)
		.filter(id => document.querySelector('.diagram .pic [data-anchor="' + id + '"]'))`))
	if len(want) < 3 {
		t.Fatalf("the fixture should draw several statements, got %v", want)
	}
	stops := get(t, ctx, chromedp.Evaluate[int](`document.querySelectorAll('.diagram .pic [data-anchor][tabindex="0"]').length`))
	if stops != 1 {
		t.Fatalf("the picture should be one Tab stop with a roving focus, found %d", stops)
	}

	active := func() (string, string) {
		got := get(t, ctx, chromedp.Evaluate[[]string](`[document.activeElement.dataset.anchor || "", document.activeElement.getAttribute("aria-label") || ""]`))
		return got[0], got[1]
	}
	get(t, ctx, chromedp.Evaluate[any](`document.querySelector('.diagram .pic [data-anchor][tabindex="0"]').focus()`))
	var target string
	for i, id := range want {
		if i > 0 {
			do(t, ctx, chromedp.KeyEvent(kb.Tab))
		}
		got, label := active()
		if got != id {
			t.Fatalf("stop %d: focus on %q, want %q (source order %v)", i, got, id, want)
		}
		if !strings.HasPrefix(label, "Statement ") {
			t.Fatalf("stop %d (%s) has no accessible name: %q", i, id, label)
		}
		if strings.HasSuffix(id, "/db") {
			target = id
			break
		}
	}
	if target == "" {
		t.Fatal("never reached the db statement")
	}

	// Enter opens the composer on that statement; Escape gives focus back.
	do(t, ctx,
		chromedp.KeyEvent(kb.Enter),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
		chromedp.KeyEvent(kb.Escape),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	if got, _ := active(); got != target {
		t.Fatalf("after Escape focus is on %q, want the shape %q", got, target)
	}

	do(t, ctx,
		chromedp.KeyEvent(kb.Enter),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
		chromedp.SendKeys(chromedp.CSS(".composer textarea"), "Which database?"),
		chromedp.Focus(chromedp.CSS(".composer .save")),
		chromedp.KeyEvent(kb.Enter),
	)
	evs := r.events(t, 1)
	if evs[0].Block != target || evs[0].Text != "Which database?" {
		t.Fatalf("note landed on %q with %q, want %q", evs[0].Block, evs[0].Text, target)
	}
}
