package browsertest

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/session"
)

const handlerGo = `package ingest

// Ingest stores a batch.
func Ingest(n int) error {
	if n > 0 {
		return retry(n)
	}
	return nil
}
`

// Issue #31: a declaration in a source file renders its own source, so a
// few lines selected inside a function are offered as a note of their own,
// saved with a sub-anchor, and marked in place once saved.
func TestSourceSelectionSavesASubAnchoredNote(t *testing.T) {
	r := serve(t, "handler.go", handlerGo, session.Options{})
	ctx := browser(t)
	id := r.block(t, "func", "Ingest")
	if id != "Ingest" {
		t.Fatalf("block id %q, want the symbol path Ingest", id)
	}

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(sel(id)),
		chromedp.Evaluate[chromedp.Void](`(function(){
			var el = document.querySelector('[data-block="Ingest"] code');
			var walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
			var n, range = document.createRange(), started = false;
			while((n = walker.nextNode())){
				var i = n.nodeValue.indexOf("retry(n)");
				if(i >= 0){ range.setStart(n, i); range.setEnd(n, i + "retry(n)".length); started = true; break; }
			}
			if(!started) throw new Error("no retry(n) text");
			var s = window.getSelection(); s.removeAllRanges(); s.addRange(range);
		})()`),
		chromedp.WaitVisible(chromedp.CSS(".selbtn")),
		chromedp.Evaluate[chromedp.Void](`document.querySelector(".selbtn").dispatchEvent(new MouseEvent("mousedown", {bubbles: true}))`),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
		chromedp.SendKeys(chromedp.CSS(".composer textarea"), "returns before the retry"),
		chromedp.Click(chromedp.CSS(".composer .save")),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	ev := r.events(t, 1)[0]
	if ev.Block != "Ingest" || ev.Sub == nil || ev.Sub.Quote != "retry(n)" {
		t.Fatalf("saved %s, want a note on Ingest anchored to retry(n)", dump(r.events(t, 1)))
	}

	do(t, ctx, chromedp.Navigate(r.URL), chromedp.WaitVisible(sel(id, "mark.mg-sub")))
	marked := get(t, ctx, chromedp.Evaluate[string](`document.querySelector('[data-block="Ingest"] mark.mg-sub').textContent`))
	if strings.TrimSpace(marked) != "retry(n)" {
		t.Errorf("marked %q, want retry(n)", marked)
	}
}
