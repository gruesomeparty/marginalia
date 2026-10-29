package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

func wait(t *testing.T, s *Server, query string) waitResult {
	t.Helper()
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/wait"+query, nil))
	if rr.Code != 200 {
		t.Fatalf("wait%s: code=%d body=%s", query, rr.Code, rr.Body.String())
	}
	var res waitResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// The acceptance criterion of issue #44: a caller holding the long-poll hears
// about a saved comment within a second of it being saved.
func TestWaitAnswersWhenACommentIsSaved(t *testing.T) {
	s, _, _ := newTestServer(t)
	start := wait(t, s, "")
	if start.Events != 0 || start.Done || start.TimedOut {
		t.Fatalf("a fresh server reports %+v", start)
	}

	got := make(chan waitResult, 1)
	go func() { got <- wait(t, s, "?since=0&timeout=10") }()
	time.Sleep(100 * time.Millisecond) // let the wait park
	saved := time.Now()
	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: "comment", Text: "hm"}); rr.Code != 201 {
		t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
	}
	select {
	case res := <-got:
		if res.Events != 1 || res.TimedOut {
			t.Errorf("wait returned %+v, want one event", res)
		}
		if d := time.Since(saved); d > time.Second {
			t.Errorf("wait answered %s after the save, want under a second", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not answer after a save")
	}
}

func TestWaitTimesOutWithNothingNew(t *testing.T) {
	s, _, _ := newTestServer(t)
	if res := wait(t, s, "?since=0&timeout=0"); !res.TimedOut || res.Events != 0 {
		t.Errorf("wait with nothing new = %+v, want timed_out", res)
	}
	for _, q := range []string{"?since=x", "?timeout=-1"} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/wait"+q, nil))
		if rr.Code != 400 {
			t.Errorf("wait%s: code=%d, want 400", q, rr.Code)
		}
	}
}

// Done closes on this server's review_done, and not on one an earlier round
// left in the log: that was the last handover's answer.
func TestDoneCountsOnlyThisRun(t *testing.T) {
	s, store, _ := newTestServer(t)
	if err := store.Append(feedback.Event{Type: feedback.TypeReviewDone}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
		t.Fatal("a review_done from before the server started closed Done")
	default:
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Type: feedback.TypeReviewDone}); rr.Code != 201 {
		t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close after review_done")
	}
	if res := wait(t, s, "?since=99"); !res.Done || res.TimedOut {
		t.Errorf("a done review should answer a wait at once, got %+v", res)
	}
}

// In a set, Done waits for every document, and session_done does them all.
func TestDoneWaitsForEveryDocumentInASet(t *testing.T) {
	s, _ := newSetServer(t)
	first := s.docs[0].Rel
	if rr := post(t, s, "/api/feedback?doc="+first, feedback.Event{Type: feedback.TypeReviewDone}); rr.Code != 201 {
		t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
	}
	if done, total := s.Progress(); done != 1 || total < 2 {
		t.Fatalf("progress = %d of %d", done, total)
	}
	select {
	case <-s.Done():
		t.Fatal("Done closed with documents still open")
	default:
	}
	if rr := post(t, s, "/api/session_done", nil); rr.Code != 201 {
		t.Fatalf("session_done: %d %s", rr.Code, rr.Body.String())
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close after session_done")
	}
}
