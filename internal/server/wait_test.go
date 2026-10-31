package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
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

// A parked long-poll must not hold the shutdown drain: Shutdown does not
// cancel request contexts, so without an answer the drain runs into its
// deadline and Serve reports an error where it should have stopped cleanly.
func TestShutdownAnswersAParkedWait(t *testing.T) {
	s, _, _ := newTestServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, ln) }()

	answered := make(chan int, 1)
	go func() {
		res, err := http.Get("http://" + ln.Addr().String() + "/api/wait?since=0&timeout=60")
		if err != nil {
			answered <- -1
			return
		}
		_ = res.Body.Close()
		answered <- res.StatusCode
	}()
	time.Sleep(200 * time.Millisecond) // let the wait park
	stopped := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v with a wait parked, want a clean stop", err)
		}
		if d := time.Since(stopped); d > 2*time.Second {
			t.Errorf("shutdown took %s with a wait parked", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
	}
	if code := <-answered; code != http.StatusOK {
		t.Errorf("the parked wait got %d, want an answer", code)
	}
}

// With complete_when: all, one reviewer's Done records only their own
// completion; the review is done once everyone listed has said so (#53).
func TestDoneFollowsTheCompletionRule(t *testing.T) {
	s, _ := framed(t, config(t, "complete_when: all\nreviewers: [ana, bo]\n"))
	if rr := post(t, s, "/api/feedback", feedback.Event{Type: feedback.TypeReviewDone, Author: "ana"}); rr.Code != 201 {
		t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
	}
	select {
	case <-s.Done():
		t.Fatal("one of two reviewers finished the review")
	default:
	}
	var res feedback.Resolution
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/resolution", nil))
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Done || res.DoneBy != 1 || res.DoneNeeded != 2 {
		t.Errorf("resolution after ana: done %v, %d of %d", res.Done, res.DoneBy, res.DoneNeeded)
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Type: feedback.TypeReviewDone, Author: "bo"}); rr.Code != 201 {
		t.Fatalf("post: %d %s", rr.Code, rr.Body.String())
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close once both reviewers finished")
	}
}
