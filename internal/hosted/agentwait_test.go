package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
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
	human := map[string]string{DefaultHumanHeader: e.mint(e.key, e.claims("berkay@example.com"))}
	note := func(ts *httptest.Server, typ string) {
		t.Helper()
		body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: typ, Text: "x"})
		if code, out := do(t, "POST", ts.URL+"/r/"+r.Meta.ID+"/api/feedback", body, human); code != http.StatusCreated {
			t.Fatalf("%s: %d %s", typ, code, out)
		}
	}
	wait := func(ts *httptest.Server, since string) string {
		t.Helper()
		_, out := do(t, "GET", ts.URL+"/agent/r/"+r.Meta.ID+"/wait?since="+since+"&timeout=5", nil, agent)
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
	h2, ts2 := open() // a restart: a new process over the same store
	if out := wait(ts2, "3"); !strings.Contains(out, `"events":3`) || !strings.Contains(out, `"done":true`) {
		t.Fatalf("after a restart: %s", out)
	}

	// The next round: the last round's review_done does not end its wait.
	if _, err := h2.Republish(context.Background(), r.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte(specDoc + "\nMore again.\n")}}}); err != nil {
		t.Fatal(err)
	}
	_, ts3 := open() // the round's start is stored, so a restart keeps it
	for _, ts := range []*httptest.Server{ts2, ts3} {
		_, out := do(t, "GET", ts.URL+"/agent/r/"+r.Meta.ID+"/wait?since=3&timeout=1", nil, agent)
		if !strings.Contains(out, `"done":false`) || !strings.Contains(out, "timed_out") {
			t.Fatalf("a republish after review_done: %s", out)
		}
	}
	note(ts3, feedback.TypeReviewDone)
	if out := wait(ts3, "3"); !strings.Contains(out, `"events":4`) || !strings.Contains(out, `"done":true`) {
		t.Fatalf("this round's review_done: %s", out)
	}
}

// A republish that drops a document keeps its events in the count: the
// cursor an agent holds must never be above what the next note makes it.
func TestAgentWaitCountsADroppedDocument(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)}, Document{Name: "notes.md", Content: []byte(specDoc)})
	agent := map[string]string{"Authorization": "Bearer tok"}
	human := map[string]string{DefaultHumanHeader: e.mint(e.key, e.claims("berkay@example.com"))}
	note := func(doc string) {
		t.Helper()
		body, _ := json.Marshal(feedback.Event{Doc: doc, Block: "1/2", Type: "comment", Text: "x"})
		if code, out := do(t, "POST", ts.URL+"/r/"+r.Meta.ID+"/api/feedback", body, human); code != http.StatusCreated {
			t.Fatalf("%s: %d %s", doc, code, out)
		}
	}
	note("spec.md")
	note("notes.md")
	for i, docs := range [][]Document{
		{{Name: "spec.md", Content: []byte(specDoc + "\nMore.\n")}},
		{{Name: "spec.md", Content: []byte(specDoc)}, {Name: "notes.md", Content: []byte(specDoc)}}, // and back
	} {
		if _, err := h.Republish(context.Background(), r.Meta.ID, Publish{Docs: docs}); err != nil {
			t.Fatal(err)
		}
		since := strconv.Itoa(2 + i)
		got := make(chan string, 1)
		go func() {
			_, out := do(t, "GET", ts.URL+"/agent/r/"+r.Meta.ID+"/wait?since="+since+"&timeout=5", nil, agent)
			got <- out
		}()
		time.Sleep(100 * time.Millisecond)
		note("spec.md")
		if out := <-got; !strings.Contains(out, `"events":`+strconv.Itoa(3+i)) || strings.Contains(out, "timed_out") {
			t.Fatalf("revision %d: %s", i+2, out)
		}
	}
}

// A review archived while a wait is parked is gone, not broken.
func TestAgentWaitOnAnArchivedReview(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	got := make(chan int, 1)
	go func() {
		code, _ := do(t, "GET", ts.URL+"/agent/r/"+r.Meta.ID+"/wait?since=0&timeout=5", nil, map[string]string{"Authorization": "Bearer tok"})
		got <- code
	}()
	time.Sleep(100 * time.Millisecond)
	if err := h.archive(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if code := <-got; code != http.StatusNotFound {
		t.Errorf("archived mid-wait: %d, want 404", code)
	}
}
