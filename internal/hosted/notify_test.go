package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// receiver is an ntfy-shaped endpoint that remembers what it got.
type receiver struct {
	t     *testing.T
	srv   *httptest.Server
	mu    sync.Mutex
	posts []delivery
	// hang makes the receiver stall for this long; status makes it answer
	// with that code.
	hang   time.Duration
	status int
}

type delivery struct {
	body    string
	headers http.Header
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{t: t, status: 200}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		hang, status := r.hang, r.status
		r.mu.Unlock()
		if hang > 0 {
			select {
			case <-time.After(hang):
			case <-req.Context().Done():
				return
			}
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(req.Body)
		r.mu.Lock()
		r.posts = append(r.posts, delivery{body: b.String(), headers: req.Header.Clone()})
		r.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) got() []delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delivery(nil), r.posts...)
}

// wait gives background deliveries a moment, then returns what arrived.
func (r *receiver) wait(n int) []delivery {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.got()) >= n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // and catch anything extra
	return r.got()
}

func openNotifying(t *testing.T, rc *receiver, format string, clock *time.Time) (*Host, *httptest.Server, *Notifier) {
	t.Helper()
	n := &Notifier{URLs: []string{rc.srv.URL}, Format: format, Timeout: 300 * time.Millisecond, Now: func() time.Time { return *clock }, Log: testWriter{t}}
	h, err := Open(context.Background(), Options{
		Store: storage.NewMemory(), PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com",
		Log: testWriter{t}, Now: func() time.Time { return *clock }, Notify: n,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(n.Close)
	return h, ts, n
}

const secretDoc = "# Plan\n\nThe launch code is CORRELATION-HORSE.\n"

func TestPublishingNotifiesOnceWithTheURLAndNoContent(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	rc := newReceiver(t)
	h, _, _ := openNotifying(t, rc, "text", &clock)
	r, err := h.Publish(context.Background(), Publish{Title: "Launch plan", Agent: "agent:claude", Docs: []Document{{Name: "plan.md", Content: []byte(secretDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	posts := rc.wait(1)
	if len(posts) != 1 {
		t.Fatalf("got %d POSTs, want exactly 1", len(posts))
	}
	p := posts[0]
	url := "https://review.example/r/" + r.Meta.ID + "/"
	if !strings.Contains(p.body, url) || p.headers.Get("Click") != url {
		t.Errorf("the review URL is missing: body %q, Click %q", p.body, p.headers.Get("Click"))
	}
	if !strings.Contains(p.body, "Launch plan") || !strings.Contains(p.body, "agent:claude") || !strings.Contains(p.body, "published") {
		t.Errorf("the body does not say what happened: %q", p.body)
	}
	if p.headers.Get("Title") != "Launch plan" {
		t.Errorf("Title header = %q", p.headers.Get("Title"))
	}
	if strings.Contains(p.body, "CORRELATION-HORSE") || strings.Contains(p.body, "launch code") {
		t.Fatalf("the document's content leaked into the notification: %q", p.body)
	}
}

func TestJSONFormat(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	rc := newReceiver(t)
	h, _, _ := openNotifying(t, rc, "json", &clock)
	r, err := h.Publish(context.Background(), Publish{Title: "Launch plan", Agent: "agent:claude", Docs: []Document{{Name: "plan.md", Content: []byte(secretDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	posts := rc.wait(1)
	if len(posts) != 1 || posts[0].headers.Get("Content-Type") != "application/json" {
		t.Fatalf("posts: %+v", posts)
	}
	var ev Notification
	if err := json.Unmarshal([]byte(posts[0].body), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Event != NotifyPublished || ev.ReviewID != r.Meta.ID || ev.Title != "Launch plan" || ev.Agent != "agent:claude" || ev.URL != "https://review.example/r/"+r.Meta.ID+"/" || ev.At != "2026-10-14T09:00:00Z" {
		t.Errorf("json = %+v", ev)
	}
	if strings.Contains(posts[0].body, "CORRELATION-HORSE") {
		t.Fatal("content leaked")
	}
}

// Five republishes inside the window produce one or two POSTs, not five:
// the first goes at once, the rest fold into one "n updates" that leaves
// once the window has passed.
func TestBurstsCollapse(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	rc := newReceiver(t)
	h, _, n := openNotifying(t, rc, "text", &clock)
	r, err := h.Publish(context.Background(), Publish{Title: "spec", Agent: "a", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		clock = clock.Add(5 * time.Second)
		if _, err := h.Republish(context.Background(), r.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte(specDoc + "\n" + strings.Repeat("x", i+1) + "\n")}}}); err != nil {
			t.Fatal(err)
		}
		n.Flush()
	}
	if got := rc.wait(1); len(got) != 1 {
		t.Fatalf("inside the window: %d POSTs, want 1", len(got))
	}
	// The window passes: the burst goes out as one notification saying how
	// many it stands for.
	clock = clock.Add(time.Minute)
	n.Flush()
	posts := rc.wait(2)
	if len(posts) != 2 {
		t.Fatalf("after the window: %d POSTs, want 2", len(posts))
	}
	if !strings.Contains(posts[1].body, "5 updates") || !strings.Contains(posts[1].body, "new revision") {
		t.Errorf("the collapsed notification: %q", posts[1].body)
	}
	// Nothing more comes out of nothing.
	clock = clock.Add(time.Minute)
	n.Flush()
	if got := rc.wait(2); len(got) != 2 {
		t.Errorf("a flush with nothing pending sent %d", len(got)-2)
	}
}

// Human actions never notify; an agent's reply after the human finished does.
func TestHumansNeverNotifyAgentsAfterDoneDo(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	rc := newReceiver(t)
	h, ts, n := openNotifying(t, rc, "text", &clock)
	r, err := h.Publish(context.Background(), Publish{Title: "spec", Agent: "a", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	rc.wait(1)
	clock = clock.Add(2 * time.Minute)
	page := ts.URL + "/r/" + r.Meta.ID + "/"
	for _, e := range []feedback.Event{
		{Doc: "spec.md", Block: "1/2", Type: "question", Text: "why 500? the secret is HUNTER2"},
		{Doc: "spec.md", Type: feedback.TypeReviewDone},
	} {
		if code := post(t, page+"api/feedback", e); code != http.StatusCreated {
			t.Fatal(code)
		}
	}
	n.Flush()
	if got := rc.wait(1); len(got) != 1 {
		t.Fatalf("a human's comment and Done produced %d notification(s)", len(got)-1)
	}
	// The agent answers after Done: one notification, with no note text.
	clock = clock.Add(2 * time.Minute)
	cs := connect(t, ts)
	var seen feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"review_id": r.Meta.ID}, &seen); err != nil {
		t.Fatal(err)
	}
	if err := call(t, cs, "reply_to_note", map[string]any{"review_id": r.Meta.ID, "name": "spec.md", "note_id": seen.Docs[0].Events[0].ID, "text": "because of the batch limit, see SECRET-PLAN"}, nil); err != nil {
		t.Fatal(err)
	}
	posts := rc.wait(2)
	if len(posts) != 2 {
		t.Fatalf("the agent's reply after Done produced %d notification(s), want 1", len(posts)-1)
	}
	if !strings.Contains(posts[1].body, "replied after you finished") || strings.Contains(posts[1].body, "SECRET-PLAN") || strings.Contains(posts[1].body, "HUNTER2") {
		t.Errorf("the reply notification: %q", posts[1].body)
	}
	// A claim before Done on another review is the loop still in the
	// agent's hands: no notification.
	r2, err := h.Publish(context.Background(), Publish{Title: "other", Agent: "a", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	rc.wait(3)
	if code := post(t, ts.URL+"/r/"+r2.Meta.ID+"/api/feedback", feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "n"}); code != http.StatusCreated {
		t.Fatal(code)
	}
	var seen2 feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"review_id": r2.Meta.ID}, &seen2); err != nil {
		t.Fatal(err)
	}
	if err := call(t, cs, "mark_addressed", map[string]any{"review_id": r2.Meta.ID, "name": "spec.md", "note_id": seen2.Docs[0].Events[0].ID, "text": "did it"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := rc.wait(3); len(got) != 3 {
		t.Errorf("a claim before Done notified: %d", len(got)-3)
	}
}

// A receiver that hangs or fails never slows or fails a publish beyond the
// timeout, and a failure is a line in the log rather than an error.
func TestABrokenReceiverNeverFailsAPublish(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	for name, broken := range map[string]func(*receiver){
		"hangs":       func(r *receiver) { r.hang = 5 * time.Second },
		"returns 500": func(r *receiver) { r.status = 500 },
	} {
		t.Run(name, func(t *testing.T) {
			rc := newReceiver(t)
			broken(rc)
			h, _, n := openNotifying(t, rc, "text", &clock)
			start := time.Now()
			if _, err := h.Publish(context.Background(), Publish{Title: "spec", Agent: "a", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}}); err != nil {
				t.Fatalf("the publish failed: %v", err)
			}
			if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
				t.Errorf("the publish waited %s on the receiver", elapsed)
			}
			// Delivery gives up after the timeout and one retry, and the
			// process does not hang on it.
			done := make(chan struct{})
			go func() { n.Close(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("delivery did not give up")
			}
		})
	}
}

func TestNoNotifierIsNoOp(t *testing.T) {
	var n *Notifier
	n.Notify(Notification{Event: NotifyPublished})
	n.Flush()
	n.Close()
	n = &Notifier{}
	n.Notify(Notification{Event: NotifyPublished})
	if got := Text(Notification{Event: NotifyReplied, Agent: "agent:x", Title: "T", URL: "u", Updates: 3}); !strings.Contains(got, "agent:x replied after you finished: T (3 updates)\nu\n") {
		t.Errorf("Text = %q", got)
	}
}
