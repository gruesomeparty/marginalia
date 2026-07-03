package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

func TestServeGracefulShutdown(t *testing.T) {
	s, store, _ := newTestServer(t)
	_ = store
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- s.Serve(ctx, ln) }()

	// Server is up: GET / should return the page.
	url := "http://" + ln.Addr().String()
	resp, err := http.Get(url + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after ctx cancel")
	}
}

func TestGetFeedbackReturnsEvents(t *testing.T) {
	s, store, _ := newTestServer(t)
	_ = store.Append(feedback.Event{Block: "1/1", Type: "comment", Text: "hi", Ts: "2026-07-03T10:00:00Z"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/feedback", nil))
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	var got []feedback.Event
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "hi" {
		t.Fatalf("got %+v", got)
	}
}

func TestPostReviewDoneAccepted(t *testing.T) {
	s, store, _ := newTestServer(t)
	body, _ := json.Marshal(feedback.Event{Type: "review_done"})
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/feedback", bytes.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Load()
	if len(got) != 1 || got[0].Type != "review_done" {
		t.Fatalf("got %+v", got)
	}
}

func TestHandlersReturn500OnCorruptStore(t *testing.T) {
	s, store, _ := newTestServer(t)
	if err := os.WriteFile(store.Path(), []byte("{not valid json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/api/doc", "/api/feedback"} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("GET %s: code=%d, want 500", path, rr.Code)
		}
	}
}
