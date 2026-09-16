package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

const specDoc = "# Spec\n\nCapped at 500 for now.\n\n## Retry\n\nThree attempts, no backoff.\n"

// serveMCP wires a client to a server over the in-memory transport, so the
// tests exercise the real tool schemas and dispatch without a subprocess.
func serveMCP(t *testing.T, root string) (*mcp.ClientSession, *Server) {
	t.Helper()
	srv, err := New(Options{Root: root, Log: testWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	clientT, serverT := mcp.NewInMemoryTransports()
	go func() { _ = srv.MCP().Run(context.Background(), serverT) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, srv
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Logf("server: %s", p); return len(p), nil }

// call runs a tool and decodes its structured result into out.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) error {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err
	}
	if res.IsError {
		return errText(res)
	}
	if out != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
	return nil
}

func errText(res *mcp.CallToolResult) error {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return toolError(b.String())
}

type toolError string

func (e toolError) Error() string { return string(e) }

func writeDoc(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestToolsAreTheOnesWeMeantToShip(t *testing.T) {
	cs, _ := serveMCP(t, t.TempDir())
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description — an agent picks tools by these", tool.Name)
		}
	}
	want := []string{"review_document", "feedback_since", "review_status", "reply_to_note", "await_review_done", "close_review"}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
	// reply_to_note is the one tool that writes, and it writes one type. An
	// agent must never be able to put words in the reviewer's mouth — no
	// comment, no verdict, and above all no review_done, which is the signal
	// it is waiting on. Answering a question is the exception the whole
	// feature is, and it is legible as one: a reply names the note it answers
	// and can be nothing else.
	for name := range got {
		for _, forbidden := range []string{"post", "append", "feedback_write", "session_done", "apply"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("tool %q looks like it writes the human's answers — only reply_to_note writes, and only replies", name)
			}
		}
	}
	if len(got) != len(want) {
		t.Errorf("tool surface grew: %v", got)
	}
}

// The whole handover, as an agent would drive it.
func TestReviewRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "spec.md", specDoc)
	cs, _ := serveMCP(t, root)

	var started reviewDocumentOut
	if err := call(t, cs, "review_document", map[string]any{"paths": []string{"spec.md"}, "review": "adr"}, &started); err != nil {
		t.Fatal(err)
	}
	if started.URL == "" || started.SessionID == "" {
		t.Fatalf("no page to hand over: %+v", started)
	}
	// The vocabulary comes back with the URL, so the agent knows the words it
	// will read out of the log before the human writes any.
	var hasBlocker bool
	for _, a := range started.Actions {
		if a.Type == "blocker" {
			hasBlocker = true
		}
	}
	if !hasBlocker {
		t.Errorf("the framing's vocabulary did not come back: %+v", started.Actions)
	}
	if !strings.Contains(started.ServeCommand, "--review adr") {
		t.Errorf("no way to restart the review by hand: %q", started.ServeCommand)
	}

	res, err := http.Get(started.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		t.Fatalf("the page is not being served: %d", res.StatusCode)
	}

	// The path the tool handed back, not the one we passed in: they differ
	// wherever a path component is a symlink.
	doc := started.Docs[0].Doc
	post(t, started.URL, feedback.Event{Doc: doc, Block: "1/2", Type: "blocker", Text: "500 is asserted, not derived", Author: "berkay"})

	var seen feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"session_id": started.SessionID}, &seen); err != nil {
		t.Fatal(err)
	}
	if len(seen.Docs) != 1 || len(seen.Docs[0].Events) != 1 {
		t.Fatalf("expected one event, got %+v", seen.Docs)
	}
	if seen.Docs[0].Events[0].Type != "blocker" {
		t.Errorf("the configured action did not survive the round trip: %+v", seen.Docs[0].Events[0])
	}

	// A cursor is how an agent avoids re-reading what it already handled.
	var again feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{
		"session_id": started.SessionID,
		"cursor":     map[string]int{doc: seen.Docs[0].Cursor},
	}, &again); err != nil {
		t.Fatal(err)
	}
	if len(again.Docs[0].Events) != 0 {
		t.Errorf("the cursor did not hold: %d events came back twice", len(again.Docs[0].Events))
	}

	var status statusOut
	if err := call(t, cs, "review_status", map[string]any{"session_id": started.SessionID}, &status); err != nil {
		t.Fatal(err)
	}
	if status.Docs[0].Comments != 1 || status.Docs[0].Done {
		t.Errorf("status wrong: %+v", status.Docs[0])
	}

	// An unfinished review times out rather than erroring, and says so.
	var waited awaitOut
	start := time.Now()
	if err := call(t, cs, "await_review_done", map[string]any{"session_id": started.SessionID, "timeout_seconds": 1}, &waited); err != nil {
		t.Fatalf("a timeout must not be an error: %v", err)
	}
	if waited.Done || waited.Reason != "timeout" {
		t.Errorf("expected an honest timeout, got %+v", waited)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the timeout was not honoured: waited %s", elapsed)
	}

	post(t, started.URL, feedback.Event{Doc: doc, Type: feedback.TypeReviewDone, Author: "berkay"})
	var finished awaitOut
	start = time.Now()
	if err := call(t, cs, "await_review_done", map[string]any{"session_id": started.SessionID, "timeout_seconds": 30}, &finished); err != nil {
		t.Fatal(err)
	}
	if !finished.Done || finished.Reason != "review_done" {
		t.Fatalf("the finished review was not noticed: %+v", finished)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("a finished review should return at once, took %s", elapsed)
	}

	var closed closeOut
	if err := call(t, cs, "close_review", map[string]any{"session_id": started.SessionID}, &closed); err != nil {
		t.Fatal(err)
	}
	if !closed.Stopped || !closed.Docs[0].Done {
		t.Errorf("close did not report the finished review: %+v", closed)
	}
	if _, err := http.Get(started.URL); err == nil {
		t.Error("the review server is still listening after close_review")
	}

	// And everything still reads with no server at all, which is what makes
	// the session's death survivable.
	var afterwards statusOut
	if err := call(t, cs, "review_status", map[string]any{"paths": []string{"spec.md"}}, &afterwards); err != nil {
		t.Fatal(err)
	}
	if afterwards.Docs[0].Comments != 1 || !afterwards.Docs[0].Done {
		t.Errorf("disk-backed read lost the review: %+v", afterwards.Docs[0])
	}
}

func post(t *testing.T, url string, e feedback.Event) {
	t.Helper()
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(url+"/api/feedback", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s: %d", e.Type, res.StatusCode)
	}
}

// A tool argument can come from text the model read, and .json/.yaml are
// supported inputs — so a path outside the root must never be parsed, let
// alone served.
func TestPathsAreConfinedToTheRoot(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "spec.md", specDoc)
	outside := writeDoc(t, t.TempDir(), "secrets.yaml", "token: hunter2\n")
	cs, srv := serveMCP(t, root)

	for _, path := range []string{outside, "../secrets.yaml", filepath.Join(root, "..", "secrets.yaml")} {
		err := call(t, cs, "review_document", map[string]any{"paths": []string{path}}, nil)
		if err == nil {
			t.Errorf("%s was served from outside the root", path)
			continue
		}
		if !strings.Contains(err.Error(), "root") {
			t.Errorf("the refusal does not explain itself: %v", err)
		}
	}
	// Nothing was started, so nothing is listening.
	srv.mu.Lock()
	n := len(srv.sessions)
	srv.mu.Unlock()
	if n != 0 {
		t.Errorf("%d session(s) opened for refused paths", n)
	}
	// A path inside the root is fine.
	if err := call(t, cs, "review_document", map[string]any{"paths": []string{"spec.md"}}, &reviewDocumentOut{}); err != nil {
		t.Errorf("a path inside the root was refused: %v", err)
	}
}

func TestUnsupportedInputAdvertisesTheFeedbackLoop(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "notes.rtf", "{\\rtf1}")
	cs, _ := serveMCP(t, root)
	err := call(t, cs, "review_document", map[string]any{"paths": []string{"notes.rtf"}}, nil)
	if err == nil {
		t.Fatal("an unsupported format was accepted")
	}
	if !strings.Contains(err.Error(), "request-feature") {
		t.Errorf("the error does not feed the self-improvement loop: %v", err)
	}
}

// An unknown session says the thing that actually unblocks the caller: the
// paths work whether or not a server is running.
func TestUnknownSessionPointsAtPaths(t *testing.T) {
	cs, _ := serveMCP(t, t.TempDir())
	err := call(t, cs, "feedback_since", map[string]any{"session_id": "nope"}, nil)
	if err == nil {
		t.Fatal("an unknown session was accepted")
	}
	if !strings.Contains(err.Error(), "paths") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestCursorSurvivesAShortenedLog(t *testing.T) {
	dir := t.TempDir()
	doc := writeDoc(t, dir, "spec.md", specDoc)
	store := feedback.NewStore(doc)
	if err := store.Append(feedback.Event{Doc: doc, Block: "1/1", Type: "comment", Text: "one"}); err != nil {
		t.Fatal(err)
	}
	// A cursor past the end can only mean the append-only rule was broken.
	// Starting over is the honest answer; skipping silently is not.
	ev, err := eventsSince(doc, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Events) != 1 || ev.Cursor != 1 {
		t.Errorf("an impossible cursor lost events: %+v", ev)
	}
}

// On macOS /var is a symlink to /private/var, so the path a caller passes and
// the path the tools hand back are different strings for the same file. The
// tools must speak one identity, and a cursor keyed by either spelling must
// still mean the same place in the log — a cursor that silently matches
// nothing re-delivers the whole log on every call, which is the worst way for
// one to fail.
func TestPathIdentitySurvivesASymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	writeDoc(t, real, "spec.md", specDoc)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable here")
	}
	// t.TempDir itself sits under a symlink on macOS (/var -> /private/var),
	// so the expectation has to be resolved too — the first version of this
	// test had the very bug it exists to catch.
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := serveMCP(t, link)

	var started reviewDocumentOut
	if err := call(t, cs, "review_document", map[string]any{"paths": []string{"spec.md"}}, &started); err != nil {
		t.Fatal(err)
	}
	served := started.Docs[0].Doc
	if served != filepath.Join(resolved, "spec.md") {
		t.Fatalf("the tool reported %q, want the resolved %q", served, filepath.Join(resolved, "spec.md"))
	}
	post(t, started.URL, feedback.Event{Doc: served, Block: "1/2", Type: "comment", Text: "one", Author: "t"})

	var seen feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"paths": []string{"spec.md"}}, &seen); err != nil {
		t.Fatal(err)
	}
	if len(seen.Docs[0].Events) != 1 {
		t.Fatalf("expected the event, got %d", len(seen.Docs[0].Events))
	}
	// The caller keys its cursor by the path it knows — the unresolved one.
	// That must still mean "I have read this much".
	var again feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{
		"paths":  []string{"spec.md"},
		"cursor": map[string]int{filepath.Join(link, "spec.md"): seen.Docs[0].Cursor},
	}, &again); err != nil {
		t.Fatal(err)
	}
	if len(again.Docs[0].Events) != 0 {
		t.Errorf("a cursor keyed by the caller's own spelling was ignored: %d events came back twice", len(again.Docs[0].Events))
	}
}
