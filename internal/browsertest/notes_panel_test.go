package browsertest

import (
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// The all-notes panel lists every note in document order through the page's
// own filter, lists a note whose block is gone at the end, and a click on an
// entry lands on its block (issue #130).
func TestNotesPanelListsEveryNote(t *testing.T) {
	r := serve(t, "plan.md", prose, session.Options{})
	friday := r.block(t, "paragraph", "Friday")
	migration := r.block(t, "paragraph", "migration")
	for _, e := range []feedback.Event{
		{Block: migration, Type: "question", Text: "How long does it take?"},
		{Block: friday, Type: "comment", Text: "Which Friday?"},
		{Block: "9/9", Quote: "a paragraph that was deleted", Hash: "000000000000", Type: "comment", Text: "Gone now."},
	} {
		e.Doc, e.Author, e.Ts = r.Doc.Path, "reviewer", "2026-10-29T00:00:00Z"
		if e.Hash == "" {
			for _, b := range r.Doc.Blocks {
				if b.ID == e.Block {
					e.Quote, e.Hash = b.Quote, b.Hash
				}
			}
		}
		if err := r.Store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	ctx := browser(t)
	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(sel(friday)),
		chromedp.Click(chromedp.CSS("#notes-toggle")),
		chromedp.WaitVisible(chromedp.CSS("#notes-panel .entry")),
	)
	audit(t, ctx, "notes panel open")
	texts := get(t, ctx, chromedp.Evaluate[[]string](`[...document.querySelectorAll("#notes-panel .entry .text")].map(e => e.textContent)`))
	want := []string{"Which Friday?", "How long does it take?", "Gone now."}
	if len(texts) != len(want) {
		t.Fatalf("panel lists %q, want %q", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("panel lists %q, want document order then the orphan: %q", texts, want)
		}
	}
	if n := get(t, ctx, chromedp.Evaluate[int](`document.querySelectorAll("#notes-panel .entry.orphan").length`)); n != 1 {
		t.Fatalf("%d entries marked as no longer anchored, want 1", n)
	}

	// An entry goes to its block.
	do(t, ctx, chromedp.Click(chromedp.CSS("#notes-panel li:nth-child(2) .entry")))
	do(t, ctx, chromedp.WaitReady(chromedp.CSS(`.block.landed[data-block="`+migration+`"]`)))

	// The page's filter is the panel's filter: hiding answered blocks hides
	// their notes here too, and the panel says so.
	do(t, ctx,
		chromedp.Click(chromedp.CSS("#filter summary")),
		chromedp.Click(chromedp.CSS(`#filters button[data-filter="unanswered"]`)),
		chromedp.WaitVisible(chromedp.CSS("#notes-hidden")),
	)
	left := get(t, ctx, chromedp.Evaluate[int](`document.querySelectorAll("#notes-panel .entry:not(.orphan)").length`))
	if left != 0 {
		t.Fatalf("%d notes on answered blocks still listed under the Not answered filter", left)
	}

	// The panel stays under the header when the header grows — it wraps to
	// another row when a label in it changes — so its close button is never
	// left underneath.
	gap := get(t, ctx, chromedp.Evaluate[float64](`(async () => {
		document.querySelector("header").style.paddingBottom = "3rem";
		await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
		return document.getElementById("notes-panel").getBoundingClientRect().top - document.querySelector("header").getBoundingClientRect().bottom;
	})()`, chromedp.EvalAwaitPromise))
	if gap < -0.5 || gap > 0.5 {
		t.Fatalf("panel top is %.1fpx from the header's bottom after the header grew", gap)
	}

	// Open is remembered across a reload, and so is closed, like the
	// reviewer's other display choices.
	do(t, ctx, chromedp.Reload(), chromedp.WaitVisible(chromedp.CSS("#notes-panel .entry")))

	do(t, ctx,
		chromedp.Click(chromedp.CSS("#notes-close")),
		chromedp.WaitNotVisible(chromedp.CSS("#notes-panel")),
		chromedp.Reload(),
		chromedp.WaitVisible(sel(friday)),
	)
	if open := get(t, ctx, chromedp.Evaluate[bool](`!document.getElementById("notes-panel").hidden`)); open {
		t.Fatal("the panel reopened after it was closed")
	}
}
