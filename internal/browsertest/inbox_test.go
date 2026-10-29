package browsertest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	cfgpkg "github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/server"
)

// mounted serves one document the way a hosted instance does — under
// /r/<id>, told its mount and its inbox — beside a stand-in inbox at "/".
func mounted(t *testing.T, cfg *cfgpkg.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, []byte(prose), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	s := server.New(server.Options{
		Doc: doc, Store: feedback.NewStore(path), Review: cfg,
		Author: "reviewer", Log: io.Discard,
		BasePath: "/r/abc", InboxURL: "/",
	})
	mux := http.NewServeMux()
	mux.Handle("/r/abc/", http.StripPrefix("/r/abc", s.Handler()))
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<!doctype html><title>inbox</title><h1 id="inbox">Inbox</h1>`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL + "/r/abc/"
}

// The header's link and `i` both lead from a hosted review back to the inbox.
func TestHostedReviewGoesBackToTheInbox(t *testing.T) {
	url := mounted(t, nil)
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(chromedp.CSS("#back")),
		chromedp.Click(chromedp.CSS("#back")),
		chromedp.WaitVisible(chromedp.CSS("#inbox")),
	)

	do(t, ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(chromedp.CSS("#back")),
		chromedp.KeyEvent("i"),
		chromedp.WaitVisible(chromedp.CSS("#inbox")),
	)
}

// A review that configured `i` keeps it: the shortcut yields, the link stays.
func TestInboxKeyYieldsToAConfiguredAction(t *testing.T) {
	cfg := cfgpkg.Default()
	if err := cfg.OverlayYAML("actions:\n  - type: idea\n    label: Idea\n    key: i\n"); err != nil {
		t.Fatal(err)
	}
	url := mounted(t, cfg)
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(chromedp.CSS("#back")),
		chromedp.KeyEvent("i"),
	)
	time.Sleep(300 * time.Millisecond) // long enough for a navigation to have started
	loc := get(t, ctx, chromedp.Evaluate[string](`location.pathname`))
	if !strings.HasPrefix(loc, "/r/abc/") {
		t.Errorf("`i` left the review for %s although the review claimed the key", loc)
	}
}
