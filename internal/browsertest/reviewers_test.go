package browsertest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// Two reviewers on one page (#53): another reviewer's note appears without a
// reload, every note says who wrote it and your own say "you", and Done
// counts towards the requester's rule.
func TestAnotherReviewersNoteArrives(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "review.yaml")
	if err := os.WriteFile(cfg, []byte("complete_when: all\nreviewers: [ana, bo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := serve(t, "plan.md", prose, session.Options{Author: "ana", Config: cfg})
	ctx := browser(t)
	id := r.block(t, "paragraph", "Friday")

	approve := chromedp.CSS(`.composer .chip[data-type="approve"]`)
	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.Click(sel(id)),
		chromedp.WaitVisible(approve),
		chromedp.Click(approve),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	r.events(t, 1)
	if got := get(t, ctx, chromedp.Text(chromedp.CSS("#done"))); got != "Done (0 of 2)" {
		t.Errorf("Done reads %q, want the rule's tally", got)
	}

	// Bo, in another browser, rejects the same block.
	body, _ := json.Marshal(feedback.Event{Doc: r.Doc.Path, Block: id, Type: feedback.TypeReject, Text: "not Friday", Author: "bo"})
	res, err := http.Post(strings.TrimSuffix(r.URL, "/")+"/api/feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bo's note: %d", res.StatusCode)
	}

	var by []string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		by = get(t, ctx, chromedp.Evaluate[[]string](`Array.from(document.querySelectorAll('[data-block="`+id+`"] + .existing .by')).map(function(e){return e.textContent})`))
		if len(by) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(by) != 2 || by[0] != "ana (you)" || by[1] != "bo" {
		t.Fatalf("notes attributed %q, want ana (you) then bo", by)
	}
}
