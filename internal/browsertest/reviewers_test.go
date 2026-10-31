package browsertest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// In a set under complete_when: all, a reviewer's Done counts but does not
// tick the document; the tick appears once the rule is met, without a reload.
func TestSetTickFollowsTheCompletionRule(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{"a.md": prose, "b.md": prose} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(t.TempDir(), "review.yaml")
	if err := os.WriteFile(cfg, []byte("complete_when: all\nreviewers: [ana, bo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := session.Build([]string{dir}, session.Options{Author: "ana", Config: cfg, Diagrams: "off", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	ctx := browser(t)

	tick := `document.querySelector('.nav a[aria-current="page"]').parentElement.querySelector('.tick').hidden`
	do(t, ctx,
		chromedp.Navigate(ts.URL+"/"),
		chromedp.Click(chromedp.CSS("#done")),
		chromedp.WaitVisible(chromedp.CSS("#sheet-submit")),
		chromedp.Click(chromedp.CSS("#sheet-submit")),
	)
	var label string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if label = get(t, ctx, chromedp.Text(chromedp.CSS("#done"))); strings.HasSuffix(label, "(1 of 2)") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.HasSuffix(label, "(1 of 2)") {
		t.Fatalf("Done reads %q after ana's Done", label)
	}
	if !get(t, ctx, chromedp.Evaluate[bool](tick)) {
		t.Fatal("one of two reviewers' Done ticked the document")
	}

	body, _ := json.Marshal(feedback.Event{Doc: filepath.Join(dir, "a.md"), Type: feedback.TypeReviewDone, Author: "bo"})
	res, err := http.Post(ts.URL+"/api/feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bo's Done: %d", res.StatusCode)
	}
	hidden := true
	for deadline = time.Now().Add(8 * time.Second); time.Now().Before(deadline) && hidden; time.Sleep(100 * time.Millisecond) {
		hidden = get(t, ctx, chromedp.Evaluate[bool](tick))
	}
	if hidden {
		t.Error("both reviewers done, the tick never appeared")
	}
}
