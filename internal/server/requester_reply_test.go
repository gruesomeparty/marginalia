package server

import (
	"net/http"
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
