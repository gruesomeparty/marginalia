package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

const askedConfig = `
notes:
  - id: cap
    block: "1/2"
    text: is 500 deliberate?
  - block: "1/2"
    text: populate or drop?
`

// The acceptance criterion of issue #90: given a requester note on block B,
// the reviewer replies to that note and the log holds a reply whose reply_to
// is the note's id — not merely an event on B, which with two notes on one
// block would leave the agent guessing which question it answers.
func TestReplyToARequesterNoteNamesIt(t *testing.T) {
	cfg := config(t, askedConfig)
	s, store := framed(t, cfg)
	second := cfg.Notes[1].ID

	for _, to := range []string{"req:cap", second} {
		rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: "reply", Text: "Yes.", ReplyTo: to})
		if rr.Code != http.StatusCreated {
			t.Fatalf("reply to %s: code=%d body=%s", to, rr.Code, rr.Body.String())
		}
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ReplyTo != "req:cap" || got[1].ReplyTo != second {
		t.Fatalf("log = %+v", got)
	}
}

// Only a reply: nobody addresses, confirms or reopens the agent's own
// question, and a requester id the config does not have is still unknown.
func TestRequesterNoteRefusesEverythingButAReply(t *testing.T) {
	s, store := framed(t, config(t, askedConfig))
	for _, e := range []feedback.Event{
		{Block: "1/2", Type: "confirm", ReplyTo: "req:cap"},
		{Block: "1/2", Type: "addressed", Text: "done", ReplyTo: "req:cap"},
		{Block: "1/2", Type: "reply", Text: "Yes.", ReplyTo: "req:nope"},
	} {
		if rr := post(t, s, "/api/feedback", e); rr.Code != http.StatusBadRequest {
			t.Errorf("%s to %s: code=%d, want 400", e.Type, e.ReplyTo, rr.Code)
		}
	}
	if got, _ := store.Load(); len(got) != 0 {
		t.Fatalf("a refused event was appended: %+v", got)
	}
}

const choiceConfig = `
notes:
  - id: db
    text: Which database?
    options: [postgres, sqlite]
`

// Issue #215: a tap on a multiple-choice note is a reply carrying the
// choice, on no block when the question is about the whole document. The
// server, not the page, decides that the choice is one the agent offered.
func TestAChoiceMustBeOffered(t *testing.T) {
	s, store := framed(t, config(t, choiceConfig+`
  - id: open
    block: "1/2"
    text: Anything else?
`))
	ok := feedback.Event{Type: "reply", Text: "sqlite", ReplyTo: "req:db", Fields: map[string]string{"choice": "sqlite"}}
	if rr := post(t, s, "/api/feedback", ok); rr.Code != http.StatusCreated {
		t.Fatalf("choice: code=%d body=%s", rr.Code, rr.Body.String())
	}
	for name, e := range map[string]feedback.Event{
		"not offered": {Type: "reply", Text: "mysql", ReplyTo: "req:db", Fields: map[string]string{"choice": "mysql"}},
		"other field": {Type: "reply", Text: "x", ReplyTo: "req:db", Fields: map[string]string{"pick": "sqlite"}},
		"no options":  {Block: "1/2", Type: "reply", Text: "no", ReplyTo: "req:open", Fields: map[string]string{"choice": "no"}},
		"addressed":   {Type: "addressed", Text: "x", ReplyTo: "req:db", Fields: map[string]string{"choice": "sqlite"}},
	} {
		if rr := post(t, s, "/api/feedback", e); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: code=%d, want 400", name, rr.Code)
		}
	}
	got, _ := store.Load()
	if len(got) != 1 {
		t.Fatalf("log holds %d events, want only the offered choice: %+v", len(got), got)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/resolution", nil))
	var res feedback.Resolution
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Orphaned != 0 || res.Replies != 1 || len(res.States) != 1 || res.States[0].Block != "" || res.States[0].History[0].Answers != "req:db" {
		t.Errorf("a document-level answer materialized as %+v", res)
	}
}
