package browsertest

import (
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/session"
)

// The composer separates what you write from what you tap (issue 133):
// verdicts sit in their own labelled group, coloured by meaning, and the
// default vocabulary fits one row on a desktop screen.
func TestVerdictsStandApartInTheComposer(t *testing.T) {
	r := serve(t, "plan.md", prose, session.Options{})
	ctx := browser(t)
	id := r.block(t, "paragraph", "Friday")

	do(t, ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(r.URL),
		chromedp.Click(sel(id)),
		chromedp.WaitVisible(chromedp.CSS(".composer .verdicts")),
	)
	type chip struct {
		Type, Group, Class string
		Top                int
	}
	chips := get(t, ctx, chromedp.Evaluate[[]chip](`Array.from(document.querySelectorAll(".composer .chip")).map(function(b){
		return {Type: b.dataset.type, Group: b.parentElement.className, Class: b.className, Top: Math.round(b.getBoundingClientRect().top)};
	})`))
	want := map[string]string{"comment": "say", "suggest_edit": "say", "question": "say", "approve": "verdicts", "reject": "verdicts"}
	if len(chips) != len(want) {
		t.Fatalf("composer has %d chips, want %d: %+v", len(chips), len(want), chips)
	}
	for _, c := range chips {
		if want[c.Type] != c.Group {
			t.Errorf("%s sits in %q, want %q", c.Type, c.Group, want[c.Type])
		}
		if c.Top != chips[0].Top {
			t.Errorf("%s wrapped to another row at 1440px (top %d, first chip %d)", c.Type, c.Top, chips[0].Top)
		}
	}
	for typ, class := range map[string]string{"approve": "chip ok", "reject": "chip bad"} {
		for _, c := range chips {
			if c.Type == typ && c.Class != class {
				t.Errorf("%s has class %q, want %q", typ, c.Class, class)
			}
		}
	}
	label := get(t, ctx, chromedp.AttributeValue(chromedp.CSS(".composer .verdicts"), "aria-label")).Value
	if label != "Verdict" {
		t.Errorf("verdict group is labelled %q, want Verdict", label)
	}
}
