package browsertest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/session"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// axe-core runs over the page in the test only (issue #155): the page itself
// still loads nothing. The package is fetched from the npm registry once,
// checked against a pinned digest and cached, so a run needs no Node and a
// tampered or moved tarball fails loudly instead of auditing with something
// else. Bumping it means changing both constants.
const (
	axeVersion = "4.10.3"
	axeSHA256  = "0f2b4d7dcdf7d1219df8d1959ad68e565f51d14c3f0d88bb71cd59abeb956292"
)

var (
	axeOnce sync.Once
	axeJS   string
	axeErr  error
)

// axeSource returns axe.min.js, or skips (fails under MARGINALIA_BROWSER=require)
// when it cannot be had.
func axeSource(t *testing.T) string {
	t.Helper()
	axeOnce.Do(func() { axeJS, axeErr = loadAxe() })
	if axeErr != nil {
		if os.Getenv("MARGINALIA_BROWSER") == "require" {
			t.Fatalf("axe-core %s: %v", axeVersion, axeErr)
		}
		t.Skipf("accessibility checks skipped: axe-core %s unavailable: %v", axeVersion, axeErr)
	}
	return axeJS
}

func loadAxe() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	cached := filepath.Join(dir, "marginalia-test", "axe-core-"+axeVersion+".min.js")
	if b, err := os.ReadFile(cached); err == nil {
		return string(b), nil
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://registry.npmjs.org/axe-core/-/axe-core-" + axeVersion + ".tgz")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry answered %s", resp.Status)
	}
	tgz, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(tgz)
	if got := hex.EncodeToString(sum[:]); got != axeSHA256 {
		return "", fmt.Errorf("tarball digest %s, want %s", got, axeSHA256)
	}
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", errors.New("no package/axe.min.js in the tarball")
		}
		if err != nil {
			return "", err
		}
		if h.Name != "package/axe.min.js" {
			continue
		}
		js, err := io.ReadAll(io.LimitReader(tr, 8<<20))
		if err != nil {
			return "", err
		}
		// The cache is a convenience; a failure to write it costs a fetch.
		if os.MkdirAll(filepath.Dir(cached), 0o755) == nil {
			_ = os.WriteFile(cached, js, 0o644)
		}
		return string(js), nil
	}
}

// violation is the part of an axe result a failure message needs.
type violation struct {
	ID     string `json:"id"`
	Impact string `json:"impact"`
	Help   string `json:"help"`
	Nodes  []struct {
		Target []any `json:"target"`
	} `json:"nodes"`
}

// audit runs axe over the page as it stands and fails on every critical or
// serious violation, naming the rule and where it fired.
func audit(t *testing.T, ctx context.Context, what string) {
	t.Helper()
	src := axeSource(t)
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[any](src+";0")); err != nil {
		t.Fatalf("inject axe: %v", err)
	}
	// Contrast is measured on what is painted, so a toast caught halfway
	// through its fade reads as faint text on the page. Let running
	// transitions finish first (capped, in case something loops).
	found := get(t, ctx, chromedp.Evaluate[[]violation](
		`new Promise(done => {
			const until = performance.now() + 2000;
			const settle = () => document.getAnimations().some(a => a.playState === "running") && performance.now() < until
				? requestAnimationFrame(settle) : done();
			settle();
		}).then(() => axe.run(document, {resultTypes: ["violations"]})).then(r => r.violations)`,
		chromedp.EvalAwaitPromise))
	for _, v := range found {
		if v.Impact != "critical" && v.Impact != "serious" {
			continue
		}
		where := make([]string, 0, len(v.Nodes))
		for i, n := range v.Nodes {
			if i == 5 {
				where = append(where, fmt.Sprintf("… %d more", len(v.Nodes)-5))
				break
			}
			where = append(where, fmt.Sprint(n.Target...))
		}
		t.Errorf("%s: %s (%s): %s — %s", what, v.ID, v.Impact, v.Help, strings.Join(where, "; "))
	}
}

const a11yDoc = "# Rollout\n\nWe ship on **Friday**, behind a flag.\n\n" +
	"## Steps\n\n1. Migrate the schema.\n2. Deploy the workers.\n   - canary first\n\n" +
	"```go\nfunc main() { fmt.Println(\"hi\") } // ship it\n```\n\n" +
	"| Step | Owner |\n|---|---|\n| migrate | db |\n\n> A quote, for the record.\n"

// The review page in the states a reviewer spends time in: reading, with the
// composer open, and with the Done sheet up.
func TestReviewPageHasNoSeriousA11yViolations(t *testing.T) {
	r := serve(t, "rollout.md", a11yDoc, session.Options{})
	ctx := browser(t)
	id := r.block(t, "paragraph", "Friday")

	do(t, ctx, chromedp.Navigate(r.URL), chromedp.WaitVisible(sel(id)))
	audit(t, ctx, "markdown page")

	do(t, ctx,
		chromedp.Click(sel(id)),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
	)
	audit(t, ctx, "composer open")

	get(t, ctx, chromedp.Evaluate[any](`[...document.querySelectorAll(".composer button")].find(b => b.textContent === "Cancel").click()`))
	do(t, ctx,
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
		chromedp.Click(chromedp.CSS("#done")),
		chromedp.WaitVisible(chromedp.CSS("#sheet")),
	)
	audit(t, ctx, "Done sheet open")
}

func TestTreePageHasNoSeriousA11yViolations(t *testing.T) {
	r := serve(t, "config.json", `{"service": {"name": "api", "replicas": 3, "ports": [80, 443]}}`, session.Options{})
	ctx := browser(t)
	do(t, ctx, chromedp.Navigate(r.URL), chromedp.WaitVisible(chromedp.CSS("[data-block]")))
	audit(t, ctx, "JSON tree")
}

func TestDiagramPageHasNoSeriousA11yViolations(t *testing.T) {
	axeSource(t) // before standInMMDC moves HOME, so the cache is the real one
	src, mmdc := standInMMDC(t)
	r := serve(t, "flow.md", "# Flow\n\n```mermaid\n"+string(src)+"```\n",
		session.Options{Diagrams: "auto", MMDC: mmdc})
	ctx := browser(t)
	do(t, ctx, chromedp.Navigate(r.URL), chromedp.WaitVisible(chromedp.CSS(".diagram .pic svg")))
	audit(t, ctx, "drawn diagram")
}

func TestInboxHasNoSeriousA11yViolations(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rows := []web.InboxRow{
		{
			ID: "a", URL: "/r/a/", Title: "Rollout plan", Agent: "agent:ci", Docs: 1, Revision: 2,
			Published: now.Add(-3 * time.Hour), LastActivity: now.Add(-time.Hour),
			Status: web.StatusInProgress, StatusLabel: "In progress", Comments: 3, Outstanding: 2, Stale: 1,
		},
		{
			ID: "b", URL: "/r/b/", Title: "Schema change", Agent: "agent:ci", Docs: 2, Revision: 1,
			Published: now.Add(-50 * time.Hour), LastActivity: now.Add(-48 * time.Hour),
			Status: web.StatusAwaiting, StatusLabel: "Awaiting",
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = web.RenderInbox(w, web.Inbox{Rows: rows, Tab: web.TabAll, Now: now})
	}))
	t.Cleanup(ts.Close)
	ctx := browser(t)
	do(t, ctx, chromedp.Navigate(ts.URL), chromedp.WaitVisible(chromedp.CSS("body")))
	audit(t, ctx, "inbox")
}

func TestStatsHasNoSeriousA11yViolations(t *testing.T) {
	a := web.AgentStats{
		Agent: "agent:ci", Reviews: 3, Done: 2, Rounds: 3,
		FirstLook: web.Durations{Count: 2, Median: 3600, P90: 7200}, TimeToDone: web.Durations{Count: 2, Median: 9000, P90: 86400},
		Notes: map[string]int{"comment": 4, "approve": 2}, Claims: web.Claims{Addressed: 2, Confirmed: 1, Pending: 1, Unclaimed: 1},
	}
	b := web.AgentStats{Agent: "agent:docs", Reviews: 1, Notes: map[string]int{}}
	total := web.AgentStats{Reviews: 4, Done: 2, Rounds: 3, FirstLook: a.FirstLook, TimeToDone: a.TimeToDone, Notes: a.Notes, Claims: a.Claims}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = web.RenderStats(w, web.Stats{Period: "30d", Total: total, Agents: []web.AgentStats{a, b}, AllAgents: []string{"agent:ci", "agent:docs"}, AllFramings: []string{"default"}})
	}))
	t.Cleanup(ts.Close)
	ctx := browser(t)
	do(t, ctx, chromedp.Navigate(ts.URL), chromedp.WaitVisible(chromedp.CSS("table")))
	audit(t, ctx, "stats")
}
