package browsertest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/session"
)

// A requester note is answered where it is asked (#90): the answer names the
// note by id, renders under it, and does not move the header count.
func TestAnswerARequesterNote(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "review.yaml")
	body := "notes:\n  - id: fixed\n    block: \"1/2\"\n    text: Is Friday fixed?\n  - block: \"1/2\"\n    text: Who decides?\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := serve(t, "plan.md", prose, session.Options{Config: cfg})
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(chromedp.CSS(".sidenote .replybtn")),
	)
	before := get(t, ctx, chromedp.Text(chromedp.CSS("#count")))
	notes := get(t, ctx, chromedp.Evaluate[[]string](`Array.from(document.querySelectorAll(".sidenote")).map(function(n){return n.textContent})`))
	if len(notes) != 2 {
		t.Fatalf("sidenotes = %q", notes)
	}
	// Answer the second question, so a match by block alone would be wrong.
	do(t, ctx,
		chromedp.Click(chromedp.CSS(`.sidenote:nth-of-type(2) .replybtn`)),
		chromedp.WaitVisible(chromedp.CSS(".sidenote .replybox textarea")),
		chromedp.SendKeys(chromedp.CSS(".sidenote .replybox textarea"), "Berkay does."),
		chromedp.Click(chromedp.CSS(".sidenote .replybox .send")),
		chromedp.WaitNotPresent(chromedp.CSS(".sidenote .replybox")),
	)
	ev := r.events(t, 1)[0]
	if ev.Type != "reply" || ev.Text != "Berkay does." || !strings.HasPrefix(ev.ReplyTo, "req:") || ev.ReplyTo == "req:fixed" {
		t.Errorf("saved %+v, want a reply to the second (derived-id) note", ev)
	}
	thread := get(t, ctx, chromedp.Text(chromedp.CSS(`.sidenote:nth-of-type(2) .thread`)))
	if !strings.Contains(thread, "Berkay does.") {
		t.Errorf("answer not shown under its note: %q", thread)
	}
	if after := get(t, ctx, chromedp.Text(chromedp.CSS("#count"))); after != before {
		t.Errorf("header count moved on an answer: %q -> %q", before, after)
	}
	// Reloaded, the answer is still under its question and not in the
	// block's own notes.
	do(t, ctx, chromedp.Navigate(r.URL), chromedp.WaitVisible(chromedp.CSS(".sidenote .thread .reply")))
	existing := get(t, ctx, chromedp.Evaluate[int](`document.querySelectorAll(".existing").length`))
	if existing != 0 {
		t.Errorf("the answer also rendered as a block note (%d .existing)", existing)
	}
}

// A multiple-choice question about the whole document (#215) heads the
// page; one tap saves one reply carrying the choice, and the pick survives
// a reload as the pressed option.
func TestTapAChoice(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "review.yaml")
	body := "notes:\n  - id: db\n    text: Which database?\n    options: [postgres, sqlite]\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := serve(t, "plan.md", prose, session.Options{Config: cfg})
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(chromedp.CSS(".sidenote.whole .choice")),
	)
	first := get(t, ctx, chromedp.Evaluate[bool](`document.querySelector("#doc .sidenote") === document.querySelector(".sidenote.whole")`))
	if !first {
		t.Error("the document-level question does not head the page")
	}
	do(t, ctx,
		chromedp.Click(chromedp.CSS(`.sidenote.whole .choice[data-choice="sqlite"]`)),
		chromedp.WaitVisible(chromedp.CSS(`.sidenote.whole .choice[data-choice="sqlite"][aria-pressed="true"]`)),
	)
	evs := r.events(t, 1)
	if ev := evs[0]; ev.Type != "reply" || ev.ReplyTo != "req:db" || ev.Fields["choice"] != "sqlite" || ev.Block != "" {
		t.Errorf("saved %+v, want one reply to req:db choosing sqlite", ev)
	}
	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(chromedp.CSS(`.sidenote.whole .choice[data-choice="sqlite"][aria-pressed="true"]`)),
	)
	if n := get(t, ctx, chromedp.Evaluate[int](`document.querySelectorAll(".existing, .orphans").length`)); n != 0 {
		t.Errorf("the answer also rendered as a note (%d)", n)
	}
}
