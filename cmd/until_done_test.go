package cmd

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

func untilDoneServer(t *testing.T) (*httptest.Server, string, func(context.Context, bool, time.Duration) error) {
	t.Helper()
	doc := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(doc, []byte("# Hi\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := buildServer([]string{doc}, serveOptions{Host: "127.0.0.1", Port: 0, Author: "tester", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	// The page reaches the same Server through its handler; Run listens on a
	// port of its own, which is what serveUntil drives.
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	run := func(ctx context.Context, untilDone bool, timeout time.Duration) error {
		return serveUntil(ctx, srv, untilDone, timeout, io.Discard)
	}
	return ts, doc, run
}

// The acceptance criterion of issue #44: --until-done returns 0 the moment
// the human presses Done, and the log is complete when it does.
func TestServeUntilDoneExitsWhenTheReviewIsDone(t *testing.T) {
	ts, doc, run := untilDoneServer(t)
	result := make(chan error, 1)
	go func() { result <- run(context.Background(), true, time.Minute) }()

	for _, body := range []string{
		`{"block":"1/2","type":"comment","text":"last word"}`,
		`{"type":"review_done"}`,
	} {
		res, err := ts.Client().Post(ts.URL+"/api/feedback", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serve --until-done returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve --until-done did not exit after review_done")
	}
	events, err := feedback.NewStore(doc).Load()
	if err != nil || len(events) != 2 || events[0].Text != "last word" {
		t.Fatalf("log after exit = %+v (%v)", events, err)
	}
}

// A timeout is a distinct exit, and says how far the review got.
func TestServeTimeoutExitsWithItsOwnCode(t *testing.T) {
	_, _, run := untilDoneServer(t)
	err := run(context.Background(), true, 200*time.Millisecond)
	var coded exitError
	if !errors.As(err, &coded) || coded.code != exitTimeout {
		t.Fatalf("timeout returned %v, want exit %d", err, exitTimeout)
	}
	if !strings.Contains(err.Error(), "0 of 1") {
		t.Errorf("timeout error %q should say how many documents were done", err)
	}
}

// Interrupted, serve exits cleanly as it always did — neither done nor timed out.
func TestServeInterruptedIsNotATimeout(t *testing.T) {
	_, _, run := untilDoneServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	if err := run(ctx, true, time.Minute); err != nil {
		t.Fatalf("interrupt returned %v, want nil", err)
	}
}
