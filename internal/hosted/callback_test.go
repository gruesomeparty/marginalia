package hosted

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// hook is an agent's callback endpoint: https, answering as example.com
// (the name httptest's certificate is for), remembering what it got.
type hook struct {
	srv   *httptest.Server
	mu    sync.Mutex
	got   []hookCall
	calls chan struct{}
}

type hookCall struct {
	body []byte
	sig  string
}

func newHook(t *testing.T) *hook {
	t.Helper()
	k := &hook{calls: make(chan struct{}, 16)}
	k.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		k.mu.Lock()
		k.got = append(k.got, hookCall{body: b, sig: r.Header.Get(SignatureHeader)})
		k.mu.Unlock()
		k.calls <- struct{}{}
	}))
	t.Cleanup(k.srv.Close)
	return k
}

// url is the hook as an agent names it: by host name, on the hook's port.
func (k *hook) url() string {
	_, port, _ := net.SplitHostPort(k.srv.Listener.Addr().String())
	return "https://example.com:" + port + "/marginalia?job=7"
}

// client reaches example.com at the hook, trusting its certificate. The
// production client would refuse: the hook is on loopback.
func (k *hook) client() *http.Client {
	c := k.srv.Client()
	tr := c.Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, k.srv.Listener.Addr().String())
	}
	c.Transport = tr
	return c
}

// wait expects exactly n more calls, then returns every call so far.
func (k *hook) wait(t *testing.T, n int) []hookCall {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-k.calls:
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d callback(s), want %d", i, n)
		}
	}
	select {
	case <-k.calls:
		t.Fatalf("more than %d callback(s)", n)
	case <-time.After(150 * time.Millisecond):
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]hookCall(nil), k.got...)
}

func openCallbacks(t *testing.T, k *hook, clock *time.Time) (*Host, *httptest.Server, *Callbacks) {
	t.Helper()
	cb, err := NewCallbacks("example.com, *.hooks.example.net", strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	cb.client, cb.Log, cb.Now = k.client(), testWriter{t}, func() time.Time { return *clock }
	h, err := Open(context.Background(), Options{
		Store: storage.NewMemory(), PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com",
		Log: testWriter{t}, Now: func() time.Time { return *clock }, Callbacks: cb,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(cb.Close)
	return h, ts, cb
}

// The acceptance criterion of #219: publish with a callback URL; Done
// delivers one POST with the review id, the event and the cursor, signed
// with the secret publish_document returned, carrying no document or note
// text; a host off the allow-list is refused at publish time.
func TestDoneCallsTheAgentBack(t *testing.T) {
	clock := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	k := newHook(t)
	h, ts, _ := openCallbacks(t, k, &clock)
	cs := connect(t, ts)
	var out publishOut
	if err := call(t, cs, "publish_document", map[string]any{
		"title":        "Launch plan",
		"documents":    []map[string]any{{"name": "plan.md", "content": secretDoc}},
		"callback_url": k.url(),
	}, &out); err != nil {
		t.Fatal(err)
	}
	if out.CallbackSecret == "" {
		t.Fatal("publish_document returned no callback_secret")
	}
	page := ts.URL + "/r/" + out.ReviewID + "/api/feedback"
	if c := post(t, page, feedback.Event{Doc: "plan.md", Block: "1/2", Type: feedback.TypeComment, Text: "CORRELATION-NOTE"}); c != http.StatusCreated {
		t.Fatalf("comment: %d", c)
	}
	if c := post(t, page, feedback.Event{Doc: "plan.md", Type: feedback.TypeReviewDone, Text: "overall CORRELATION-DONE"}); c != http.StatusCreated {
		t.Fatalf("done: %d", c)
	}
	calls := k.wait(t, 1) // a comment calls nobody
	got := calls[0]
	if !hmac.Equal([]byte(got.sig), []byte(Sign(out.CallbackSecret, got.body))) {
		t.Errorf("signature %q does not verify under the returned secret", got.sig)
	}
	var cb Callback
	if err := json.Unmarshal(got.body, &cb); err != nil {
		t.Fatal(err)
	}
	if cb.Event != feedback.TypeReviewDone || cb.ReviewID != out.ReviewID || cb.Name != "plan.md" || cb.Revision != 1 || !cb.Done || cb.Cursor["plan.md"] != 1 || cb.Since != 1 {
		t.Errorf("callback = %+v", cb)
	}
	// Reading from the cursor returns the Done the call is about.
	var read struct {
		Docs []struct {
			Events []feedback.Event `json:"events"`
		} `json:"docs"`
	}
	if err := call(t, cs, "feedback_since", map[string]any{"review_id": out.ReviewID, "cursor": cb.Cursor}, &read); err != nil {
		t.Fatal(err)
	}
	if len(read.Docs) != 1 || len(read.Docs[0].Events) != 1 || read.Docs[0].Events[0].Type != feedback.TypeReviewDone {
		t.Errorf("feedback_since from the callback's cursor = %+v, want the Done", read)
	}
	for _, leak := range []string{"CORRELATION", "launch code", "Launch plan"} {
		if strings.Contains(string(got.body), leak) {
			t.Errorf("the callback carries %q: %s", leak, got.body)
		}
	}
	// The secret is nowhere in the store.
	for _, key := range mustList(t, h) {
		if b, err := h.store.GetBlob(context.Background(), key); err == nil && strings.Contains(string(b), out.CallbackSecret) {
			t.Errorf("%s holds the callback secret", key)
		}
	}

	// Off the list, not https, or a private literal: refused before
	// anything is written.
	for _, bad := range []string{"https://evil.example.org/x", "http://example.com/x", "https://user:pw@example.com/x", "https://10.0.0.1/x", "https://hooks.example.net/x"} {
		before := len(mustList(t, h))
		err := call(t, cs, "publish_document", map[string]any{
			"documents": []map[string]any{{"name": "a.md", "content": "# a\n"}}, "callback_url": bad,
		}, nil)
		if err == nil {
			t.Errorf("%s was accepted", bad)
		}
		if after := len(mustList(t, h)); after != before {
			t.Errorf("%s: a refusal wrote %d key(s)", bad, after-before)
		}
	}
	if err := call(t, cs, "publish_document", map[string]any{
		"documents": []map[string]any{{"name": "a.md", "content": "# a\n"}}, "callback_url": "https://ci.hooks.example.net/x",
	}, nil); err != nil {
		t.Errorf("a subdomain of a wildcard entry was refused: %v", err)
	}
}

func mustList(t *testing.T, h *Host) []string {
	t.Helper()
	keys, err := h.store.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// Questions call back too, a burst collapses into one call with the latest
// cursor once the window passes, and a Done is never held back.
func TestQuestionsCallBackDebounced(t *testing.T) {
	clock := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	k := newHook(t)
	h, ts, cb := openCallbacks(t, k, &clock)
	r, err := h.Publish(context.Background(), Publish{Title: "t", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}, Callback: k.url()})
	if err != nil {
		t.Fatal(err)
	}
	page := ts.URL + "/r/" + r.Meta.ID + "/api/feedback"
	ask := func() {
		t.Helper()
		if c := post(t, page, feedback.Event{Doc: "spec.md", Block: "1/2", Type: feedback.TypeQuestion, Text: "why?"}); c != http.StatusCreated {
			t.Fatalf("question: %d", c)
		}
	}
	ask()
	k.wait(t, 1)
	clock = clock.Add(5 * time.Second)
	ask()
	ask()
	cb.Flush()
	k.wait(t, 0) // nothing new: the two wait
	clock = clock.Add(time.Minute)
	cb.Flush()
	calls := k.wait(t, 1)
	var last Callback
	_ = json.Unmarshal(calls[1].body, &last)
	// The burst's cursor is its first question's: reading from it returns both.
	if last.Event != feedback.TypeQuestion || last.Cursor["spec.md"] != 1 || last.Done {
		t.Errorf("the collapsed call = %+v", last)
	}
	clock = clock.Add(time.Second)
	ask()
	if c := post(t, page, feedback.Event{Doc: "spec.md", Type: feedback.TypeReviewDone}); c != http.StatusCreated {
		t.Fatalf("done: %d", c)
	}
	calls = k.wait(t, 1) // the Done went at once and took the question's place
	var done Callback
	_ = json.Unmarshal(calls[2].body, &done)
	// It took the waiting question's place, and its cursor: reading from it
	// returns the question and the Done.
	if done.Event != feedback.TypeReviewDone || done.Cursor["spec.md"] != 3 {
		t.Errorf("done call = %+v", done)
	}
	clock = clock.Add(time.Hour)
	cb.Flush()
	if calls = k.wait(t, 0); len(calls) != 3 {
		t.Errorf("the question the Done replaced went out anyway: %d calls", len(calls))
	}

	// A republish without a callback keeps the review's.
	r, err = h.Republish(context.Background(), r.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte(specDoc + "\nmore\n")}}})
	if err != nil || r.Meta.Callback != k.url() {
		t.Fatalf("republish: %v, callback %q", err, r.Meta.Callback)
	}
}

// Without an allow-list the feature is off, and says so; an allow-list
// without a strong secret does not start.
func TestCallbacksOffWithoutAnAllowList(t *testing.T) {
	if c, err := NewCallbacks("", ""); c != nil || err != nil {
		t.Errorf("no hosts: %v, %v", c, err)
	}
	if _, err := NewCallbacks("example.com", "short"); err == nil || !strings.Contains(err.Error(), "MARGINALIA_CALLBACK_SECRET") {
		t.Errorf("a short secret: %v", err)
	}
	h, _ := open(t, storage.NewMemory())
	_, err := h.Publish(context.Background(), Publish{Docs: []Document{{Name: "a.md", Content: []byte("# a\n")}}, Callback: "https://example.com/x"})
	if err == nil || !strings.Contains(err.Error(), "MARGINALIA_CALLBACK_HOSTS") {
		t.Errorf("a callback with callbacks off: %v", err)
	}
}

// The production dialer refuses what is not public after resolution: the
// hook on loopback is unreachable however the URL names it.
func TestTheDialerRefusesInternalAddresses(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.16.0.1": false,
		"169.254.169.254": false, "100.100.1.1": false, "::1": false, "fd00::1": false,
		"0.0.0.0": false, "::ffff:127.0.0.1": false, "fe80::1": false,
		"0.1.2.3": false, "198.18.0.1": false, "240.0.0.1": false, "64:ff9b::a00:1": false, "2002:a00:1::1": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("public(%s) = %v, want %v", addr, got, want)
		}
	}
	k := newHook(t)
	c := &Callbacks{Hosts: []string{"example.com"}, Secret: []byte(strings.Repeat("s", 32)), Timeout: time.Second}
	err := c.attempt(k.srv.URL+"/x", []byte("{}"), "sha256=x")
	if err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Errorf("the guarded client reached loopback: %v", err)
	}
}
