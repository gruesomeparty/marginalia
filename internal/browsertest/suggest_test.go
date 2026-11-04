package browsertest

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/session"
)

// The page side of issue #218: select the second of three sentences, pick
// Suggest, and the composer starts from that sentence; the saved event
// carries the sentence anchor, and the note reads as a redline of it.
func TestSuggestAnEditToOneSentence(t *testing.T) {
	r := serve(t, "plan.md", "# Plan\n\nOne is first. Two is second. Three is third.\n", session.Options{})
	ctx := browser(t)
	id := r.block(t, "paragraph", "Two is second")

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(sel(id)),
		chromedp.Evaluate[chromedp.Void](`(function(){
			var el = document.querySelector('[data-block="`+id+`"]');
			var walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT), n;
			while((n = walk.nextNode())){
				var i = n.textContent.indexOf("Two is second.");
				if(i < 0) continue;
				var r = document.createRange(); r.setStart(n, i); r.setEnd(n, i + "Two is second.".length);
				var s = window.getSelection(); s.removeAllRanges(); s.addRange(r);
				return;
			}
		})()`),
		chromedp.WaitVisible(chromedp.CSS(".selbtn")),
		chromedp.Click(chromedp.CSS(".selbtn")),
		chromedp.WaitVisible(chromedp.CSS(`.composer .chip[data-type="suggest_edit"]`)),
		chromedp.Click(chromedp.CSS(`.composer .chip[data-type="suggest_edit"]`)),
	)
	start := get(t, ctx, chromedp.Evaluate[string](`document.querySelector(".composer textarea").value`))
	if start != "Two is second." {
		t.Errorf("the suggestion starts from %q, want the selected sentence", start)
	}
	do(t, ctx,
		chromedp.Evaluate[chromedp.Void](`document.querySelector(".composer textarea").value = "Two comes second."`),
		chromedp.Click(chromedp.CSS(".composer .save")),
		chromedp.WaitVisible(chromedp.CSS(".existing .redline")),
	)
	ev := r.events(t, 1)[0]
	if ev.Type != "suggest_edit" || ev.Text != "Two comes second." || ev.Sub == nil || ev.Sub.Quote != "Two is second." {
		t.Fatalf("saved %+v, want a sentence suggestion", ev)
	}
	del := get(t, ctx, chromedp.Evaluate[string](`Array.from(document.querySelectorAll(".existing .redline del")).map(function(e){return e.textContent}).join("|")`))
	ins := get(t, ctx, chromedp.Evaluate[string](`Array.from(document.querySelectorAll(".existing .redline ins")).map(function(e){return e.textContent}).join("|")`))
	if strings.TrimSpace(del) != "is" || strings.TrimSpace(ins) != "comes" {
		t.Errorf("redline struck %q and inserted %q, want is → comes", del, ins)
	}
	// Reloaded, the redline comes back from the log.
	do(t, ctx, chromedp.Navigate(r.URL), chromedp.WaitVisible(chromedp.CSS(".existing .redline ins")))
}
