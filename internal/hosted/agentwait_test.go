package hosted

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
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
