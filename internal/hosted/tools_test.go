package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
)

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// connect opens an MCP session against the host's /mcp over Streamable
// HTTP, as a remote agent would.
func connect(t *testing.T, ts *httptest.Server) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:   ts.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: "tok", next: http.DefaultTransport}},
		MaxRetries: -1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "remote-agent", Version: "1"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call runs a tool and decodes its structured result into out.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) error {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err
	}
	if res.IsError {
		var b strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return toolError(b.String())
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

type toolError string

func (e toolError) Error() string { return string(e) }

func TestHostedToolsAreTheOnesWeMeantToShip(t *testing.T) {
	_, ts := open(t, storage.NewMemory())
	cs := connect(t, ts)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	want := []string{"publish_document", "feedback_since", "review_status", "reply_to_note", "mark_addressed", "await_review_done", "close_review"}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
	if got["review_document"] {
		t.Error("review_document is registered: a path means nothing to an agent on another machine")
	}
	if len(got) != len(want) {
		t.Errorf("tool surface grew: %v", got)
	}
}

// The acceptance criterion of #96, over the MCP HTTP client.
func TestPublishReviseAndReadBack(t *testing.T) {
	counting := storagetest.NewCounting(storage.NewMemory())
	h, ts := open(t, counting)
	cs := connect(t, ts)

	// Publish, then see the blocks at the URL that came back.
	var published publishOut
	if err := call(t, cs, "publish_document", map[string]any{
		"title": "spec", "review": "adr",
		"documents": []map[string]any{{"name": "spec.md", "content": specDoc}},
	}, &published); err != nil {
		t.Fatal(err)
	}
	if published.ReviewID == "" || published.Revision != 1 || len(published.Docs) != 1 || published.Docs[0].Blocks == 0 {
		t.Fatalf("publish returned %+v", published)
	}
	if !strings.HasPrefix(published.URL, "https://review.example/r/") {
		t.Errorf("URL %q is not built from the public URL", published.URL)
	}
	var hasBlocker bool
	for _, a := range published.Actions {
		hasBlocker = hasBlocker || a.Type == "blocker"
	}
	if !hasBlocker {
		t.Errorf("the framing's vocabulary did not come back: %+v", published.Actions)
	}
	local := ts.URL + "/r/" + published.ReviewID + "/"
	code, body := get(t, local)
	if code != 200 || !strings.Contains(body, "Capped at 500") {
		t.Fatalf("the published page: %d", code)
	}
	code, body = get(t, local+"api/doc")
	if code != 200 || !strings.Contains(body, `"1/2"`) {
		t.Fatalf("/api/doc: %d %s", code, body[:min(len(body), 200)])
	}

	// Comment on a block that will be edited, and on one that will be gone.
	// The page sends the block's hash with a note; so does this.
	first := feedback.Event{Doc: "spec.md", Block: "1/2", Quote: "Capped at 500 for now.", Hash: hashOf(t, local, "1/2"), Type: "blocker", Text: "500 is asserted, not derived", Author: "berkay"}
	if code := post(t, local+"api/feedback", first); code != http.StatusCreated {
		t.Fatalf("POST first: %d", code)
	}
	gone := feedback.Event{Doc: "spec.md", Block: "1.1/2", Hash: hashOf(t, local, "1.1/2"), Type: "question", Text: "why no backoff?", Author: "berkay"}
	if code := post(t, local+"api/feedback", gone); code != http.StatusCreated {
		t.Fatalf("POST second: %d", code)
	}
	var seen feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"review_id": published.ReviewID}, &seen); err != nil {
		t.Fatal(err)
	}
	if len(seen.Docs) != 1 || len(seen.Docs[0].Events) != 2 || seen.Docs[0].Events[0].Type != "blocker" {
		t.Fatalf("feedback_since: %+v", seen.Docs)
	}
	noteID := seen.Docs[0].Events[1].ID
	var again feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"review_id": published.ReviewID, "cursor": map[string]int{"spec.md": seen.Docs[0].Cursor}}, &again); err != nil {
		t.Fatal(err)
	}
	if len(again.Docs[0].Events) != 0 {
		t.Errorf("the cursor did not hold: %d events came back twice", len(again.Docs[0].Events))
	}

	// Answer and claim, in hosted mode.
	var answered mcpserverReply
	if err := call(t, cs, "reply_to_note", map[string]any{"review_id": published.ReviewID, "name": "spec.md", "note_id": noteID, "text": "it retries at the queue"}, &answered); err != nil {
		t.Fatal(err)
	}
	if answered.ReplyTo != noteID || answered.Block != "1.1/2" {
		t.Errorf("reply landed wrong: %+v", answered)
	}
	if err := call(t, cs, "mark_addressed", map[string]any{"review_id": published.ReviewID, "name": "spec.md", "note_id": seen.Docs[0].Events[0].ID, "text": "derived it from the batch limit"}, nil); err != nil {
		t.Fatal(err)
	}

	// Republish: the first block edited, the Retry section removed.
	counting.Reset()
	var revised publishOut
	if err := call(t, cs, "publish_document", map[string]any{
		"review_id": published.ReviewID,
		"documents": []map[string]any{{"name": "spec.md", "content": "# Spec\n\nCapped at 500, from the queue's batch limit.\n"}},
	}, &revised); err != nil {
		t.Fatal(err)
	}
	if revised.ReviewID != published.ReviewID || revised.Revision != 2 {
		t.Fatalf("republish returned %+v", revised)
	}
	if got := counting.Writes(); got != 2 {
		t.Errorf("a one-document revision cost %d writes, want 2 (the snapshot and the metadata)", got)
	}
	// The page now renders the new revision; revision 1 is byte-identical to
	// what was first published.
	if _, body := get(t, local); !strings.Contains(body, "batch limit") || strings.Contains(body, "Three attempts") {
		t.Error("the page is not rendering the new revision")
	}
	rev1, err := counting.GetBlob(context.Background(), "reviews/"+published.ReviewID+"/rev/1/spec.md")
	if err != nil || string(rev1) != specDoc {
		t.Errorf("revision 1 changed: %q, %v", rev1, err)
	}
	if _, err := counting.GetBlob(context.Background(), "reviews/"+published.ReviewID+"/rev/2/spec.md"); err != nil {
		t.Errorf("revision 2 is not in the store: %v", err)
	}

	// Status: the first note stale (its block changed), the second orphaned
	// (its block is gone), and the agent's events counted separately.
	var status statusOut
	if err := call(t, cs, "review_status", map[string]any{"review_id": published.ReviewID}, &status); err != nil {
		t.Fatal(err)
	}
	st := status.Docs[0]
	if st.Stale != 1 || st.Orphaned != 1 || st.Comments != 2 || st.Replies != 1 || st.Addressed != 1 {
		t.Errorf("status after republish: stale=%d orphaned=%d comments=%d replies=%d addressed=%d", st.Stale, st.Orphaned, st.Comments, st.Replies, st.Addressed)
	}

	// await returns when the page appends review_done.
	go func() {
		time.Sleep(700 * time.Millisecond)
		post(t, local+"api/feedback", feedback.Event{Doc: "spec.md", Type: feedback.TypeReviewDone, Author: "berkay"})
	}()
	var waited awaitOut
	start := time.Now()
	if err := call(t, cs, "await_review_done", map[string]any{"review_id": published.ReviewID, "timeout_seconds": 30}, &waited); err != nil {
		t.Fatal(err)
	}
	if !waited.Done || waited.Reason != "review_done" {
		t.Fatalf("await: %+v", waited)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("await took %s", time.Since(start))
	}
	var closed closeOut
	if err := call(t, cs, "close_review", map[string]any{"review_id": published.ReviewID}, &closed); err != nil {
		t.Fatal(err)
	}
	if !closed.Docs[0].Done || closed.URL != published.URL {
		t.Errorf("close: %+v", closed)
	}
	// Closing stops nothing: the review is still at its URL, and still the
	// host's to serve.
	if code, _ := get(t, local); code != 200 {
		t.Errorf("the review is gone after close_review: %d", code)
	}
	if h.Review(published.ReviewID) == nil {
		t.Error("the host forgot the review")
	}
}

type mcpserverReply struct {
	Doc     string `json:"doc"`
	ID      string `json:"id"`
	ReplyTo string `json:"reply_to"`
	Block   string `json:"block"`
}

func TestPublishRefusalsWriteNothing(t *testing.T) {
	counting := storagetest.NewCounting(storage.NewMemory())
	_, ts := open(t, counting)
	cs := connect(t, ts)
	counting.Reset()
	for _, name := range []string{"../x.md", ".env", "a/b.md"} {
		err := call(t, cs, "publish_document", map[string]any{"documents": []map[string]any{{"name": name, "content": "# x\n"}}}, nil)
		if err == nil {
			t.Errorf("%q was accepted", name)
		}
		if got := counting.Writes(); got != 0 {
			t.Errorf("%q: %d write(s) reached the store", name, got)
		}
	}
	err := call(t, cs, "publish_document", map[string]any{"documents": []map[string]any{{"name": "notes.rst", "content": "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Errorf("an unsupported extension must name request-feature: %v", err)
	}
	if err := call(t, cs, "publish_document", map[string]any{"documents": []map[string]any{{"name": "a.md", "content": ""}}}, nil); err == nil {
		t.Error("empty content was accepted")
	}
	if err := call(t, cs, "publish_document", map[string]any{"documents": []map[string]any{{"name": "a.md", "content": "# a\n"}}, "config": "actionz: []\n"}, nil); err == nil {
		t.Error("an invalid config was accepted")
	}
	if err := call(t, cs, "publish_document", map[string]any{"review_id": "nope", "documents": []map[string]any{{"name": "a.md", "content": "# a\n"}}}, nil); err == nil {
		t.Error("a revision of an unknown review was accepted")
	}
	if keys, _ := counting.List(context.Background(), ""); len(keys) != 0 {
		t.Errorf("refusals left keys behind: %v", keys)
	}
}

// A revision survives a restart as the current one, with every earlier
// revision still in the store and the notes still anchored to the log.
func TestRevisionsSurviveRestart(t *testing.T) {
	store := storage.NewMemory()
	h, ts := open(t, store)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	local := ts.URL + "/r/" + r.Meta.ID + "/"
	if code := post(t, local+"api/feedback", feedback.Event{Doc: "spec.md", Block: "1/2", Hash: hashOf(t, local, "1/2"), Type: "comment", Text: "n", Author: "b"}); code != http.StatusCreated {
		t.Fatal(code)
	}
	if _, err := h.Republish(context.Background(), r.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte("# Spec\n\nChanged.\n")}}}); err != nil {
		t.Fatal(err)
	}
	ts.Close()
	h2, ts2 := open(t, store)
	cur := h2.Review(r.Meta.ID)
	if cur == nil || cur.Meta.Revision != 2 || cur.Meta.Title != "spec" {
		t.Fatalf("after restart: %+v", cur)
	}
	if _, body := get(t, ts2.URL+"/r/"+r.Meta.ID+"/"); !strings.Contains(body, "Changed.") {
		t.Error("the restart did not serve the latest revision")
	}
	if _, body := get(t, ts2.URL+"/r/"+r.Meta.ID+"/api/resolution"); !strings.Contains(body, `"stale":true`) {
		t.Errorf("the note did not re-anchor as stale after the restart: %s", body[:min(len(body), 300)])
	}
}

// hashOf reads a block's current hash off /api/doc, as the page would.
func hashOf(t *testing.T, local, block string) string {
	t.Helper()
	_, body := get(t, local+"api/doc")
	var doc struct {
		Doc struct {
			Blocks []struct {
				ID   string `json:"id"`
				Hash string `json:"hash"`
			} `json:"blocks"`
		} `json:"doc"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	for _, b := range doc.Doc.Blocks {
		if b.ID == block {
			return b.Hash
		}
	}
	t.Fatalf("no block %s in /api/doc", block)
	return ""
}
