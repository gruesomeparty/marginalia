package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// The log `serve` writes is the file agents tail and `import` dedupes
// against, so its bytes are pinned end to end: a POST through the real
// handler, with every field the server would fill in supplied, lands on disk
// as exactly these bytes. A storage refactor that changed one of them would
// change what every reader of the file sees.
func TestServeWritesTheSameLogBytes(t *testing.T) {
	s, store, docPath := newTestServer(t)
	events := []feedback.Event{
		{Block: "1/2", Quote: "Hello.", Type: "comment", Text: "why?", Author: "berkay", Ts: "2026-07-03T10:00:00Z"},
		{Block: "1/2", Type: "approve", Author: "berkay", Ts: "2026-07-03T10:01:00Z"},
		{Type: feedback.TypeReviewDone, Author: "berkay", Ts: "2026-07-03T10:02:00Z"},
	}
	for _, e := range events {
		body, _ := json.Marshal(e)
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/api/feedback", bytes.NewReader(body)))
		if rr.Code != http.StatusCreated {
			t.Fatalf("POST %s: %d %s", e.Type, rr.Code, rr.Body.String())
		}
	}
	got, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	// Quote and hash are the page's to send — the server records what the
	// reviewer was looking at — so an event posted without them is stored
	// without them, exactly as before.
	want := `{"doc":"` + docPath + `","block":"1/2","quote":"Hello.","hash":"","type":"comment","text":"why?","author":"berkay","ts":"2026-07-03T10:00:00Z"}` + "\n" +
		`{"doc":"` + docPath + `","block":"1/2","quote":"","hash":"","type":"approve","text":"","author":"berkay","ts":"2026-07-03T10:01:00Z"}` + "\n" +
		`{"doc":"` + docPath + `","block":"","quote":"","hash":"","type":"review_done","text":"","author":"berkay","ts":"2026-07-03T10:02:00Z"}` + "\n"
	if string(got) != want {
		t.Fatalf("the log's bytes changed:\n got: %q\nwant: %q", got, want)
	}
	if store.Path() != docPath+".feedback.jsonl" {
		t.Errorf("log path = %s", store.Path())
	}
}
