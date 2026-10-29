package browsertest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/gruesomeparty/marginalia/internal/session"
)

// still asserts the log holds exactly n events and stays that way: what is
// being proven is that nothing was written, so it waits long enough for a
// stray fetch to have landed.
func (r review) still(t *testing.T, n int) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	evs, err := r.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != n {
		t.Fatalf("log has %d events, want %d: %s", len(evs), n, dump(evs))
	}
}

func sheetOpen(t *testing.T, ctx context.Context) bool {
	t.Helper()
	return get(t, ctx, chromedp.Evaluate[bool](`document.getElementById("sheet").open`))
}

// Done opens a summary and writes nothing; Cancel and Esc both leave the log
// as it was (#129). review_done is the agent's signal to proceed, so one
// stray click must not be able to send it.
func TestDoneOpensASheetAndCancelWritesNothing(t *testing.T) {
	r := serve(t, "plan.md", prose, session.Options{})
	ctx := browser(t)
	id := r.block(t, "paragraph", "migration")

	approve := chromedp.CSS(`.composer .chip[data-type="approve"]`)
	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.Click(sel(id)),
		chromedp.WaitVisible(approve),
		chromedp.Click(approve),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	r.events(t, 1)

	do(t, ctx,
		chromedp.Click(chromedp.CSS("#done")),
		chromedp.WaitVisible(chromedp.CSS("#sheet")),
	)
	said := get(t, ctx, chromedp.Evaluate[[]string](`Array.from(document.querySelectorAll("#sheet-said li")).map(function(li){return li.textContent})`))
	if len(said) != 1 || said[0][:1] != "1" {
		t.Errorf("sheet says %q, want one line counting the one approval", said)
	}
	left := get(t, ctx, chromedp.Text(chromedp.CSS("#sheet-left")))
	if left == "" {
		t.Error("the sheet does not say what is still unanswered")
	}
	r.still(t, 1)

	// A click in the sheet's own padding is not a click on the backdrop.
	inPadding := get(t, ctx, chromedp.Evaluate[bool](`(function(){
		var s = document.getElementById("sheet"), r = s.getBoundingClientRect();
		var o = {bubbles: true, clientX: r.left + 4, clientY: r.top + 4};
		s.dispatchEvent(new MouseEvent("mousedown", o));
		s.dispatchEvent(new MouseEvent("click", o));
		return s.open;
	})()`))
	if !inPadding {
		t.Fatal("a click inside the sheet's padding closed it")
	}
	outsideCloses := get(t, ctx, chromedp.Evaluate[bool](`(function(){
		var s = document.getElementById("sheet"), r = s.getBoundingClientRect();
		var o = {bubbles: true, clientX: r.left - 10, clientY: r.top - 10};
		s.dispatchEvent(new MouseEvent("mousedown", o));
		s.dispatchEvent(new MouseEvent("click", o));
		return !s.open;
	})()`))
	if !outsideCloses {
		t.Fatal("a click on the backdrop left the sheet open")
	}
	r.still(t, 1)
	do(t, ctx,
		chromedp.Click(chromedp.CSS("#done")),
		chromedp.WaitVisible(chromedp.CSS("#sheet")),
		chromedp.Click(chromedp.CSS("#sheet-cancel")),
	)
	if sheetOpen(t, ctx) {
		t.Fatal("Cancel left the sheet open")
	}
	r.still(t, 1)

	do(t, ctx,
		chromedp.Click(chromedp.CSS("#done")),
		chromedp.WaitVisible(chromedp.CSS("#sheet")),
		chromedp.KeyEvent(kb.Escape),
	)
	if sheetOpen(t, ctx) {
		t.Fatal("Esc left the sheet open")
	}
	r.still(t, 1)
}

// Submit writes review_done, carrying the overall note as its text.
func TestSubmitSendsReviewDoneWithTheNote(t *testing.T) {
	r := serve(t, "plan.md", prose, session.Options{})
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.Click(chromedp.CSS("#done")),
		chromedp.WaitVisible(chromedp.CSS("#sheet")),
		chromedp.SendKeys(chromedp.CSS("#sheet-note"), "Looks right; ship it."),
		chromedp.Click(chromedp.CSS("#sheet-submit")),
	)
	ev := r.events(t, 1)[0]
	if ev.Type != "review_done" || ev.Text != "Looks right; ship it." {
		t.Errorf("saved %+v, want review_done carrying the note", ev)
	}
	do(t, ctx, chromedp.WaitNotVisible(chromedp.CSS("#sheet")))
}

// Under require_verdict the sheet names what is missing and will not submit;
// picking a block closes it and goes there.
func TestRequireVerdictHoldsSubmit(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "review.yaml")
	if err := os.WriteFile(cfg, []byte("require_verdict: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := serve(t, "plan.md", prose, session.Options{Config: cfg})
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.Click(chromedp.CSS("#done")),
		chromedp.WaitVisible(chromedp.CSS("#sheet")),
	)
	disabled := get(t, ctx, chromedp.Evaluate[bool](`document.getElementById("sheet-submit").disabled`))
	if !disabled {
		t.Error("Submit is enabled with blocks still wanting a verdict")
	}
	picks := get(t, ctx, chromedp.Evaluate[int](`document.querySelectorAll("#outstanding-list button").length`))
	// Nothing is read-only and nothing is answered, so every block is listed.
	if picks != len(r.Doc.Blocks) {
		t.Errorf("sheet lists %d blocks to go to, want all %d", picks, len(r.Doc.Blocks))
	}
	do(t, ctx, chromedp.Click(chromedp.CSS("#outstanding-list button")))
	if sheetOpen(t, ctx) {
		t.Error("picking a block left the sheet open")
	}
	r.still(t, 0)
}
