package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// An agent holding the /mcp credential can long-poll a review over HTTP
// (issue #186), and is woken by the reviewer's note.
func TestAgentLongPollsAReviewWithTheStaticToken(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	wait := ts.URL + "/mcp/reviews/" + r.Meta.ID + "/wait"
	agent := map[string]string{"Authorization": "Bearer tok"}

	code, out := do(t, "GET", wait, nil, agent)
	var start struct {
		Events uint64 `json:"events"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(out), &start) != nil {
		t.Fatalf("first wait: %d %s", code, out)
	}

	type answer struct {
		code int
		body string
	}
	got := make(chan answer, 1)
	go func() {
		c, b := do(t, "GET", wait+"?since=0&timeout=10", nil, agent)
		got <- answer{c, b}
	}()
	time.Sleep(100 * time.Millisecond)
	body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "x"})
	human := map[string]string{accessHeader: e.mint(e.key, e.claims("berkay@example.com"))}
	if code, out := do(t, "POST", ts.URL+"/r/"+r.Meta.ID+"/api/feedback", body, human); code != http.StatusCreated {
		t.Fatalf("note: %d %s", code, out)
	}
	select {
	case a := <-got:
		if a.code != http.StatusOK || !strings.Contains(a.body, `"events":1`) || strings.Contains(a.body, "timed_out") {
			t.Fatalf("woken with %d %s", a.code, a.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait was not woken by the reviewer's note")
	}
}

// The route admits only the agent's credential, reaches only the wait, and
// gives an agent nothing new under /r/.
func TestAgentWaitIsTheOnlyThingAnAgentGets(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	agent := map[string]string{"Authorization": "Bearer tok"}
	base := ts.URL + "/mcp/reviews/" + r.Meta.ID

	for name, headers := range map[string]map[string]string{
		"no credential":   nil,
		"wrong token":     {"Authorization": "Bearer nope"},
		"a human's login": {accessHeader: e.mint(e.key, e.claims("berkay@example.com"))},
	} {
		req, _ := http.NewRequest("GET", base+"/wait", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: %d, challenge %q", name, res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
	}
	if code, _ := do(t, "GET", ts.URL+"/mcp/reviews/"+strings.Repeat("A", 22)+"/wait", nil, agent); code != http.StatusNotFound {
		t.Errorf("an unknown review: %d", code)
	}
	for _, u := range []string{base + "/api/doc", base + "/api/feedback", base + "/"} {
		if code, _ := do(t, "GET", u, nil, agent); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s with the agent's token: %d", u, code)
		}
	}
	if code, _ := do(t, "POST", base+"/wait", []byte("{}"), agent); code == http.StatusOK {
		t.Error("the wait route took a POST")
	}
	if code, _ := do(t, "GET", ts.URL+"/r/"+r.Meta.ID+"/api/wait", nil, agent); code != http.StatusForbidden {
		t.Errorf("the page's own wait with the agent's token: %d", code)
	}
}

// With OAuth configured, an access token from the authorization server is
// as good here as on /mcp.
func TestAgentWaitTakesAnAccessToken(t *testing.T) {
	as := newEdge(t)
	h, ts := openWithOAuth(t, as, "")
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	c := as.claims("")
	c.Subject, c.Email = "client-7", ""
	code, out := do(t, "GET", ts.URL+"/mcp/reviews/"+r.Meta.ID+"/wait", nil, map[string]string{"Authorization": "Bearer " + as.mint(as.key, c)})
	if code != http.StatusOK || !strings.Contains(out, `"events"`) {
		t.Fatalf("%d %s", code, out)
	}
}

// The cursor is the log's length, so it survives a republish, which builds
// a new server for the revision, and a restart, which rebuilds them all:
// a note after either still wakes a wait parked on the earlier cursor, and
// a review finished before a restart still reads as done.
func TestAgentWaitSurvivesRepublishAndRestart(t *testing.T) {
	e := newEdge(t)
	store := storage.NewMemory()
	open := func() (*Host, *httptest.Server) {
		h, err := Open(context.Background(), Options{Store: store, PublicURL: "https://review.example", Token: "tok", HumanAuth: e.verifier(), Log: testWriter{t}})
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(h.Handler())
		t.Cleanup(ts.Close)
		return h, ts
	}
	h, ts := open()
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	agent := map[string]string{"Authorization": "Bearer tok"}
	human := map[string]string{accessHeader: e.mint(e.key, e.claims("berkay@example.com"))}
	note := func(ts *httptest.Server, typ string) {
		t.Helper()
		body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: typ, Text: "x"})
		if code, out := do(t, "POST", ts.URL+"/r/"+r.Meta.ID+"/api/feedback", body, human); code != http.StatusCreated {
			t.Fatalf("%s: %d %s", typ, code, out)
		}
	}
	wait := func(ts *httptest.Server, since string) string {
		t.Helper()
		_, out := do(t, "GET", ts.URL+"/mcp/reviews/"+r.Meta.ID+"/wait?since="+since+"&timeout=5", nil, agent)
		return out
	}

	note(ts, "comment")
	if _, err := h.Republish(context.Background(), r.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte(specDoc + "\nMore.\n")}}}); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() { got <- wait(ts, "1") }()
	time.Sleep(100 * time.Millisecond)
	note(ts, "comment")
	select {
	case out := <-got:
		if !strings.Contains(out, `"events":2`) || strings.Contains(out, "timed_out") {
			t.Fatalf("after a republish: %s", out)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("a note after a republish did not wake the wait")
	}

	note(ts, feedback.TypeReviewDone)
	_, ts2 := open() // a restart: a new process over the same store
	if out := wait(ts2, "3"); !strings.Contains(out, `"events":3`) || !strings.Contains(out, `"done":true`) {
		t.Fatalf("after a restart: %s", out)
	}
}
