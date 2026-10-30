package browsertest

import (
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/session"
)

// layout is what the phone test reads off the page in one go.
type layout struct {
	HeaderRows  int     `json:"header_rows"`
	BadgeLeft   float64 `json:"badge_left"`
	Fixed       bool    `json:"fixed"`
	SheetBottom float64 `json:"sheet_bottom"`
	Viewport    float64 `json:"viewport"`
	Focused     bool    `json:"focused"`
	About       string  `json:"about"`
}

const measure = `(() => {
  const tops = new Set([...document.querySelector("header").children]
    .filter(c => !c.hidden && getComputedStyle(c).display !== "none")
    .map(c => { const r = c.getBoundingClientRect(); return Math.round((r.top + r.bottom) / 2 / 12); }));
  const b = document.querySelector('.block[data-count]:not([data-count="0"])');
  const left = b ? b.getBoundingClientRect().left + parseFloat(getComputedStyle(b, "::before").left) : -1;
  const c = document.querySelector(".composer");
  const cs = c && getComputedStyle(c);
  return {
    header_rows: tops.size,
    badge_left: left,
    fixed: !!c && cs.position === "fixed",
    sheet_bottom: c ? c.getBoundingClientRect().bottom : 0,
    viewport: window.innerHeight,
    focused: !!c && document.activeElement === c.querySelector("textarea"),
    about: c && c.querySelector(".about") ? c.querySelector(".about").textContent : "",
  };
})()`

// On a 390×844 phone the header is one row, the note-count badge sits
// inside the screen, and the composer opens as a bottom sheet that quotes
// the block, takes the keyboard's focus, saves, and closes on a tap outside
// without writing anything (issue #131).
func TestPhoneLayout(t *testing.T) {
	r := serve(t, "plan.md", "# Release plan for the hosted reviewer, at length\n\n"+prose[len("# Plan\n\n"):], session.Options{})
	ctx := browser(t)
	id := r.block(t, "paragraph", "Friday")
	do(t, ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(sel(id)),
		chromedp.Click(sel(id)),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
	)
	m := get(t, ctx, chromedp.Evaluate[layout](measure))
	if m.HeaderRows != 1 {
		t.Errorf("header wraps to %d rows on a phone", m.HeaderRows)
	}
	if !m.Fixed || m.SheetBottom > m.Viewport+1 {
		t.Errorf("composer is not a bottom sheet: fixed=%v bottom=%.0f viewport=%.0f", m.Fixed, m.SheetBottom, m.Viewport)
	}
	if !m.Focused {
		t.Error("the sheet's textarea does not have focus")
	}
	if m.About == "" {
		t.Error("the sheet does not quote the block it is about")
	}

	do(t, ctx,
		chromedp.SendKeys(chromedp.CSS(".composer textarea"), "Which Friday?"),
		chromedp.Click(chromedp.CSS(".composer .save")),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	if evs := r.events(t, 1); evs[0].Block != id {
		t.Fatalf("note landed on %q, want %q", evs[0].Block, id)
	}
	m = get(t, ctx, chromedp.Evaluate[layout](measure))
	if m.BadgeLeft < 0 {
		t.Errorf("the note-count badge starts %.1fpx off the left edge", m.BadgeLeft)
	}

	// A tap outside keeps a typed draft rather than throwing it away…
	do(t, ctx,
		chromedp.Click(sel(id)),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
		chromedp.SendKeys(chromedp.CSS(".composer textarea"), "half a thought"),
		chromedp.MouseClickXY(200, 120),
	)
	if kept := get(t, ctx, chromedp.Evaluate[string](`(document.querySelector(".composer textarea")||{}).value || ""`)); kept != "half a thought" {
		t.Fatalf("an outside tap left the draft as %q", kept)
	}
	// …and closes an empty sheet, writing nothing.
	do(t, ctx,
		chromedp.Evaluate[chromedp.Void](`document.querySelector(".composer textarea").value = ""`),
		chromedp.MouseClickXY(200, 120),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	r.still(t, 1)

	// The short menu labels do not name the menus; their summaries do.
	names := get(t, ctx, chromedp.Evaluate[[]string](`[...document.querySelectorAll("#filter summary, #display summary")].map(s => s.getAttribute("aria-label"))`))
	if len(names) != 2 || names[0] != "Filter: Show all" || names[1] != "Display" {
		t.Errorf("menu names at phone width = %q", names)
	}
	audit(t, ctx, "phone width")
}
