package browsertest

import (
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/session"
)

const railDoc = "# Plan\n\nWe ship on Friday.\n\nThe migration runs first.\n\nThen the workers.\n"

// railBox is where a block's notes ended up, relative to the block and the
// text column.
type railBox struct {
	Position  string  `json:"position"`
	Left      float64 `json:"left"`
	Top       float64 `json:"top"`
	Bottom    float64 `json:"bottom"`
	BlockTop  float64 `json:"blockTop"`
	MainRight float64 `json:"mainRight"`
	Next      bool    `json:"next"` // the box is still the block's next sibling
}

const railBoxes = `[...document.querySelectorAll("main .existing")].map(b => {
	const r = b.getBoundingClientRect(), blk = b.previousElementSibling;
	return {position: getComputedStyle(b).position, left: r.left, top: r.top, bottom: r.bottom,
		blockTop: blk.getBoundingClientRect().top, mainRight: document.querySelector("main").getBoundingClientRect().right,
		next: !!blk.dataset.block};
})`

// On a wide screen notes sit in a margin beside their block, pushed down
// rather than overlapping, and the text keeps its spacing; on a phone they
// stay inline under the block (issue #130).
func TestNotesSitInTheMarginOnWideScreens(t *testing.T) {
	r := serve(t, "plan.md", railDoc, session.Options{})
	friday := r.block(t, "paragraph", "Friday")
	migration := r.block(t, "paragraph", "migration")
	for _, e := range []feedback.Event{
		{Block: friday, Type: "comment", Text: "Which Friday? This needs a date, a rollback plan, and an owner for each shard."},
		{Block: migration, Type: "question", Text: "How long does it take?"},
	} {
		e.Doc, e.Author, e.Ts = r.Doc.Path, "reviewer", "2026-10-29T00:00:00Z"
		for _, b := range r.Doc.Blocks {
			if b.ID == e.Block {
				e.Quote, e.Hash = b.Quote, b.Hash
			}
		}
		if err := r.Store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	ctx := browser(t)
	do(t, ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(chromedp.CSS(".existing.in-rail")),
	)
	audit(t, ctx, "margin rail")
	boxes := get(t, ctx, chromedp.Evaluate[[]railBox](railBoxes))
	if len(boxes) != 2 {
		t.Fatalf("%d note boxes, want 2", len(boxes))
	}
	for i, b := range boxes {
		if b.Position != "absolute" || b.Left < b.MainRight {
			t.Errorf("box %d is %s at left %.0f, want absolute right of the text (%.0f)", i, b.Position, b.Left, b.MainRight)
		}
	}
	if d := boxes[0].Top - boxes[0].BlockTop; d < -1 || d > 1 {
		t.Errorf("first note is %.0fpx from the top of its block", d)
	}
	if boxes[1].Top < boxes[0].Bottom {
		t.Errorf("second note (top %.0f) overlaps the first (bottom %.0f)", boxes[1].Top, boxes[0].Bottom)
	}
	// The notes take no room in the text: consecutive paragraphs sit as
	// close as they would with no notes at all.
	gap := get(t, ctx, chromedp.Evaluate[float64](`(() => {
		const ps = [...document.querySelectorAll("main [data-kind=paragraph]")];
		if(ps.length < 3) return 1e9;
		return ps[2].getBoundingClientRect().top - ps[1].getBoundingClientRect().bottom;
	})()`))
	if gap > 40 {
		t.Errorf("paragraphs are %.0fpx apart: a note is still pushing the text down", gap)
	}

	// A phone keeps notes inline, under their block.
	do(t, ctx, chromedp.EmulateViewport(390, 844), chromedp.Reload(), chromedp.WaitVisible(chromedp.CSS(".existing")))
	for i, b := range get(t, ctx, chromedp.Evaluate[[]railBox](railBoxes)) {
		if b.Position == "absolute" || !b.Next {
			t.Errorf("box %d on a phone is %s, next to its block: %v", i, b.Position, b.Next)
		}
	}
}
