package browsertest

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/hosted"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// Picking a filter in the inbox applies it without pressing Apply, and the
// filter lands in the URL, so a filtered inbox is a link.
func TestInboxFilterAppliesOnChange(t *testing.T) {
	h, err := hosted.Open(context.Background(), hosted.Options{
		Store: storage.NewMemory(), PublicURL: "https://review.example", DevIdentity: "dev@example.com", Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []hosted.Publish{
		{Title: "first", Agent: "agent:a", Docs: []hosted.Document{{Name: "a.md", Content: []byte("# A\n\nText.\n")}}},
		{Title: "second", Agent: "agent:b", Docs: []hosted.Document{{Name: "b.md", Content: []byte("# B\n\nText.\n")}}},
	} {
		if _, err := h.Publish(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(ts.URL+"/?tab=all"),
		chromedp.WaitVisible(chromedp.CSS(".filters")),
		// Set and announce the choice in one step: setting it through the
		// protocol can already fire change, and the page then navigates
		// away under a second step (seen on macOS CI).
		chromedp.Evaluate[chromedp.Void](`(function(s){s.value="agent:b";s.dispatchEvent(new Event("change",{bubbles:true}))})(document.querySelector('select[name="agent"]'))`),
		chromedp.WaitVisible(chromedp.CSS(".filters .clear")),
	)
	url := get(t, ctx, chromedp.Location())
	if !strings.Contains(url, "agent=agent%3Ab") || !strings.Contains(url, "tab=all") {
		t.Errorf("filter is not in the URL: %s", url)
	}
	titles := get(t, ctx, chromedp.Evaluate[[]string](`Array.from(document.querySelectorAll(".review .title")).map(function(e){return e.textContent})`))
	if strings.Join(titles, "|") != "second" {
		t.Errorf("filtered inbox lists %q, want only second", titles)
	}
}
