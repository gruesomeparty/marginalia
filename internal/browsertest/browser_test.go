// Package browsertest drives the review page in a real browser against a real
// server, and asserts on both the DOM and the JSONL the page appended.
//
// Every other test in the repository stops at the HTTP boundary or greps the
// template for identifier names, so the page's JavaScript — most of what a
// reviewer touches — was defended by nothing that runs it (issue #60). This
// suite is that defence. It grows one case per PR that changes the page.
//
// It needs Chrome or Chromium. Without one it skips, so `go test ./...` stays
// green on a bare machine; MARGINALIA_BROWSER=require turns the skip into a
// failure, which is what CI sets so a runner that lost its browser cannot
// pass by testing nothing. MARGINALIA_CHROME names the binary when it is not
// on PATH.
package browsertest

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/session"
)

// chrome finds a browser to drive, or skips (or fails, when CI requires one).
func chrome(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("MARGINALIA_CHROME"),
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome",
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	if os.Getenv("MARGINALIA_BROWSER") == "require" {
		t.Fatal("MARGINALIA_BROWSER=require but no Chrome or Chromium was found; set MARGINALIA_CHROME")
	}
	t.Skip("browser tests skipped: no Chrome or Chromium found (set MARGINALIA_CHROME to run them)")
	return ""
}

// browser starts a headless browser for one test and returns a context that
// drives it, bounded so a hung page fails the test rather than the run.
func browser(t *testing.T) context.Context {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome(t)),
		// Containers, CI included, run as root, where Chrome refuses to
		// start sandboxed.
		chromedp.NoSandbox,
		chromedp.WindowSize(1280, 900),
	)
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(alloc)
	// Start the browser before the clock does: a cold first launch on a CI
	// runner can take most of the budget meant for the page. The start runs
	// on the untimed context because the first run's context owns the
	// browser, and cancelling a timeout on it would kill Chrome; a launch
	// that hangs is left to go test's own -timeout.
	if err := chromedp.Do(ctx); err != nil {
		cancelCtx()
		cancelAlloc()
		t.Fatalf("starting the browser: %v", err)
	}
	ctx, cancelTimeout := context.WithTimeout(ctx, 20*time.Second)
	t.Cleanup(func() { cancelTimeout(); cancelCtx(); cancelAlloc() })
	return ctx
}

// review is one served document: the page's URL, the document as the server
// parsed it, and its feedback log.
type review struct {
	URL   string
	Doc   *document.Document
	Store *feedback.Store
}

// serve writes a document into a temp directory and serves it the way
// `marginalia serve` does, through the shared session pipeline.
func serve(t *testing.T, name, content string, opts session.Options) review {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if opts.Author == "" {
		opts.Author = "reviewer"
	}
	if opts.Diagrams == "" {
		opts.Diagrams = "off"
	}
	opts.Log = io.Discard
	s, err := session.Build([]string{path}, opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	return review{URL: ts.URL + "/", Doc: doc, Store: feedback.NewStore(path)}
}

// block returns the id of the first block of a kind whose quote contains text.
func (r review) block(t *testing.T, kind, text string) string {
	t.Helper()
	for _, b := range r.Doc.Blocks {
		if b.Kind == kind && strings.Contains(b.Quote, text) {
			return b.ID
		}
	}
	t.Fatalf("no %s block quoting %q in %s", kind, text, r.Doc.Path)
	return ""
}

// events waits for the log to hold n events and returns them. The page's
// save is a fetch, so the line lands a moment after the click.
func (r review) events(t *testing.T, n int) []feedback.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		evs, err := r.Store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) >= n || time.Now().After(deadline) {
			if len(evs) != n {
				t.Fatalf("log has %d events, want %d: %s", len(evs), n, dump(evs))
			}
			return evs
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dump(evs []feedback.Event) string {
	b, _ := json.Marshal(evs)
	return string(b)
}

// sel is the selector of a block element by its id. Ids contain characters
// CSS treats specially (`/`, `>` in a mermaid path), so it is quoted as an
// attribute value rather than escaped.
func sel(id string, within ...string) chromedp.CSS {
	return chromedp.CSS(strings.TrimSpace(`[data-block="` + strings.ReplaceAll(id, `"`, `\"`) + `"] ` + strings.Join(within, " ")))
}

// notes is the selector of what the page shows under a prose block: its notes
// sit beside the block element, not inside it.
func notes(id string, within ...string) chromedp.CSS {
	return chromedp.CSS(strings.TrimSpace(string(sel(id)) + " + .existing " + strings.Join(within, " ")))
}

func do(t *testing.T, ctx context.Context, steps ...chromedp.Action[chromedp.Void]) {
	t.Helper()
	if err := chromedp.Do(ctx, steps...); err != nil {
		t.Fatal(err)
	}
}

func get[T any](t *testing.T, ctx context.Context, a chromedp.Action[T]) T {
	t.Helper()
	v, err := chromedp.Run(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

const prose = "# Plan\n\nWe ship on Friday.\n\nThe migration runs first.\n"

// Clicking a block opens the composer with the review's vocabulary, and Save
// appends exactly the note typed, against that block, and shows it in place.
func TestComposerSavesANote(t *testing.T) {
	r := serve(t, "plan.md", prose, session.Options{})
	ctx := browser(t)
	id := r.block(t, "paragraph", "Friday")

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.Click(sel(id)),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
	)
	chips := get(t, ctx, chromedp.Evaluate[int](`document.querySelectorAll(".composer .chip").length`))
	do(t, ctx,
		chromedp.SendKeys(chromedp.CSS(".composer textarea"), "Which Friday?"),
		chromedp.Click(chromedp.CSS(".composer .save")),
		chromedp.WaitVisible(notes(id)),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	if chips < 5 {
		t.Errorf("composer offered %d actions, want the five built-ins at least", chips)
	}
	ev := r.events(t, 1)[0]
	if ev.Block != id || ev.Type != "comment" || ev.Text != "Which Friday?" || ev.Author != "reviewer" {
		t.Errorf("saved %+v, want a comment on %s reading %q", ev, id, "Which Friday?")
	}
	if ev.Hash == "" {
		t.Error("saved note carries no hash, so it could never go stale")
	}
}

// An action with nothing to fill in saves on the tap, without a Save.
func TestOneTapActionSavesOnTheTap(t *testing.T) {
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
	state := get(t, ctx, chromedp.AttributeValue(sel(id), "data-state")).Value
	ev := r.events(t, 1)[0]
	if ev.Block != id || ev.Type != "approve" || ev.Text != "" {
		t.Errorf("saved %+v, want a bare approve on %s", ev, id)
	}
	if state != "approve" {
		t.Errorf("block's data-state = %q after the tap, want approve", state)
	}
}

// A reply posts under the note it answers, names it by id, and does not move
// the header count: that measures what the reviewer said (#47).
func TestReplyRoundTrip(t *testing.T) {
	r := serve(t, "plan.md", prose, session.Options{})
	id := r.block(t, "paragraph", "Friday")
	var b document.Block
	for _, x := range r.Doc.Blocks {
		if x.ID == id {
			b = x
		}
	}
	note := feedback.Event{
		Doc: r.Doc.Path, Block: id, Quote: b.Quote, Hash: b.Hash,
		Type: "question", Text: "Is Friday fixed?", Author: "reviewer", Ts: "2026-10-29T00:00:00Z",
	}
	if err := r.Store.Append(note); err != nil {
		t.Fatal(err)
	}
	ctx := browser(t)

	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(notes(id, ".replybtn")),
	)
	before := get(t, ctx, chromedp.Text(chromedp.CSS("#count")))
	do(t, ctx,
		chromedp.Click(notes(id, ".replybtn")),
		chromedp.WaitVisible(notes(id, ".replybox textarea")),
		chromedp.SendKeys(notes(id, ".replybox textarea"), "Yes, it is."),
		chromedp.Click(notes(id, ".replybox .send")),
		chromedp.WaitVisible(notes(id, ".replybtn[aria-expanded=false]")),
	)
	evs := r.events(t, 2)
	thread := get(t, ctx, chromedp.Text(notes(id)))
	after := get(t, ctx, chromedp.Text(chromedp.CSS("#count")))
	reply := evs[1]
	if reply.Type != "reply" || reply.ReplyTo != feedback.NoteID(evs[0]) || reply.Text != "Yes, it is." {
		t.Errorf("saved %+v, want a reply to %s", reply, feedback.NoteID(evs[0]))
	}
	if !strings.Contains(thread, "Yes, it is.") {
		t.Errorf("the answer is not rendered under its note: %q", thread)
	}
	if before != after {
		t.Errorf("header count moved on a reply: %q → %q", before, after)
	}
}

// The shape you click in the drawn diagram is the statement the note lands
// on (#36). mermaid-cli is stood in for by a script that hands back the real
// render committed in internal/diagram/testdata, so the page under test draws
// exactly what mermaid draws, without Node on the runner.
func TestDiagramShapeClickLandsOnItsStatement(t *testing.T) {
	src, mmdc := standInMMDC(t)

	r := serve(t, "flow.md", "# Flow\n\n```mermaid\n"+string(src)+"```\n",
		session.Options{Diagrams: "auto", MMDC: mmdc})
	ctx := browser(t)

	var target string
	for _, b := range r.Doc.Blocks {
		if strings.HasSuffix(b.ID, "/db") {
			target = b.ID
		}
	}
	if target == "" {
		t.Fatal("flowchart.mmd no longer declares db on its own line")
	}
	shape := chromedp.CSS(`.diagram .pic g[data-anchor="` + target + `"]`)
	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(shape),
		chromedp.Click(shape),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
	)
	// Statements share the space under the figure, so the composer says
	// which one it is about.
	composerOn := get(t, ctx, chromedp.Text(chromedp.CSS(".composer .on")))
	do(t, ctx,
		chromedp.SendKeys(chromedp.CSS(".composer textarea"), "Which database?"),
		chromedp.Click(chromedp.CSS(".composer .save")),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	if want := target[strings.LastIndex(target, "/")+1:]; composerOn != want {
		t.Errorf("clicking the %s shape opened a composer on %q, want %q", target, composerOn, want)
	}
	ev := r.events(t, 1)[0]
	if ev.Block != target || ev.Text != "Which database?" {
		t.Errorf("saved %+v, want a note on %s", ev, target)
	}
}

// A sequence diagram's message is a one-pixel line with no name of its own
// (#153): clicking it still lands the note on the message the source wrote.
func TestSequenceMessageClickLandsOnItsStatement(t *testing.T) {
	src, mmdc := standInFor(t, "sequence")
	r := serve(t, "seq.md", "# Seq\n\n```mermaid\n"+string(src)+"```\n",
		session.Options{Diagrams: "auto", MMDC: mmdc})
	ctx := browser(t)

	var target string
	for _, b := range r.Doc.Blocks {
		if strings.HasSuffix(b.ID, "/A->DB") {
			target = b.ID
		}
	}
	if target == "" {
		t.Fatal("sequence.mmd no longer sends A->>DB")
	}
	hit := chromedp.CSS(`.diagram .pic line.mg-hit[data-anchor="` + target + `"]`)
	do(t, ctx,
		chromedp.Navigate(r.URL),
		chromedp.WaitVisible(chromedp.CSS(".diagram .pic svg")),
		chromedp.Click(hit),
		chromedp.WaitVisible(chromedp.CSS(".composer textarea")),
		chromedp.SendKeys(chromedp.CSS(".composer textarea"), "Which query?"),
		chromedp.Click(chromedp.CSS(".composer .save")),
		chromedp.WaitNotPresent(chromedp.CSS(".composer")),
	)
	if ev := r.events(t, 1)[0]; ev.Block != target {
		t.Errorf("saved on %q, want %s", ev.Block, target)
	}
}

// standInMMDC returns flowchart.mmd and an mmdc stand-in that hands back the
// real render committed in internal/diagram/testdata, so a page under test
// draws exactly what mermaid draws, without Node on the runner.
func standInMMDC(t *testing.T) (src []byte, mmdc string) {
	t.Helper()
	return standInFor(t, "flowchart")
}

// standInFor is standInMMDC for any diagram in internal/diagram/testdata.
func standInFor(t *testing.T, name string) (src []byte, mmdc string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the mmdc stand-in is a shell script")
	}
	src, err := os.ReadFile("../diagram/testdata/" + name + ".mmd")
	if err != nil {
		t.Fatal(err)
	}
	fixtures, _ := filepath.Glob("../diagram/testdata/" + name + "-mermaid-cli-*.svg")
	if len(fixtures) != 1 {
		t.Fatalf("want exactly one mermaid-cli fixture, found %v", fixtures)
	}
	svg, err := filepath.Abs(fixtures[0])
	if err != nil {
		t.Fatal(err)
	}
	// mmdc -i <in> -o <out> …: copy the fixture to <out>.
	mmdc = filepath.Join(t.TempDir(), "mmdc")
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then cp '" + svg + "' \"$2\"; fi; shift; done\n"
	if err := os.WriteFile(mmdc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The renderer caches by source hash in the user cache dir; keep this
	// test's render out of the developer's cache.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	return src, mmdc
}
