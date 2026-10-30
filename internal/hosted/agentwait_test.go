package hosted

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// An agent long-polls a review with its own credential, wakes when the
// reviewer saves, and reaches nothing else with it.
func TestAgentWaitWakesOnASavedNote(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	wait := ts.URL + "/agent/r/" + r.Meta.ID + "/wait"
	agent := map[string]string{"Authorization": "Bearer tok"}
	human := map[string]string{DefaultHumanHeader: e.mint(e.key, e.claims("berkay@example.com"))}

	if code, _ := do(t, "GET", wait, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("no credential: %d, want 401", code)
	}
	if code, _ := do(t, "GET", wait, nil, map[string]string{"Authorization": "Bearer nope"}); code != http.StatusUnauthorized {
		t.Errorf("wrong credential: %d, want 401", code)
	}
	if code, _ := do(t, "GET", wait, nil, human); code != http.StatusUnauthorized {
		t.Errorf("a human's token on the agent route: %d, want 401", code)
	}
	code, body := do(t, "GET", wait, nil, agent)
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, body)
	}
	var start struct {
		Events uint64 `json:"events"`
	}
	if err := json.Unmarshal([]byte(body), &start); err != nil {
		t.Fatal(err)
	}

	type answer struct {
		code int
		body string
	}
	got := make(chan answer, 1)
	go func() {
		c, b := do(t, "GET", wait+"?since="+strconv.FormatUint(start.Events, 10)+"&timeout=20", nil, agent)
		got <- answer{c, b}
	}()
	time.Sleep(100 * time.Millisecond)
	note, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "x"})
	if c, _ := do(t, "POST", ts.URL+"/r/"+r.Meta.ID+"/api/feedback", note, human); c != http.StatusCreated {
		t.Fatal(c)
	}
	select {
	case g := <-got:
		if g.code != http.StatusOK || strings.Contains(g.body, "timed_out") || !strings.Contains(g.body, `"events":`+strconv.FormatUint(start.Events+1, 10)) {
			t.Errorf("wait woke with %d %s", g.code, g.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not wake on a saved note")
	}
	if c, _ := do(t, "GET", ts.URL+"/agent/r/"+strings.Repeat("a", 22)+"/wait", nil, agent); c != http.StatusNotFound {
		t.Errorf("unknown review: %d, want 404", c)
	}
}

// The same credential is still refused on every page route, the page's own
// wait included, and the agent prefix serves nothing but the wait.
func TestAgentCredentialStaysOffThePageRoutes(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	agent := map[string]string{"Authorization": "Bearer tok"}
	for _, p := range []string{"/r/" + r.Meta.ID + "/api/wait", "/r/" + r.Meta.ID + "/api/feedback", "/r/" + r.Meta.ID + "/"} {
		if code, _ := do(t, "GET", ts.URL+p, nil, agent); code != http.StatusForbidden {
			t.Errorf("GET %s with an agent credential: %d, want 403", p, code)
		}
	}
	for _, p := range []string{"/agent/r/" + r.Meta.ID + "/api/feedback", "/agent/r/" + r.Meta.ID + "/", "/agent/r/" + r.Meta.ID + "/wait/x"} {
		if code, _ := do(t, "GET", ts.URL+p, nil, agent); code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", p, code)
		}
	}
	if code, _ := do(t, "POST", ts.URL+"/agent/r/"+r.Meta.ID+"/wait", []byte("{}"), agent); code == http.StatusOK {
		t.Errorf("POST to the agent wait route was accepted")
	}
}
