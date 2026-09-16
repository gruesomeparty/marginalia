package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
)

// seedQuestion puts one question in the log and returns the id a reply names.
func seedQuestion(t *testing.T, store *feedback.Store) string {
	t.Helper()
	e := feedback.Event{Block: "1/2", Type: "question", Text: "Why 500?", Author: "berkay", Ts: "2026-07-03T10:00:00Z"}
	if err := store.Append(e); err != nil {
		t.Fatal(err)
	}
	return feedback.NoteID(e)
}

// The acceptance criterion of issue #47: a question from a human was a dead
// end, and an answer now lands in the same log as an event pointing at it.
func TestReplyToAKnownNoteIsAccepted(t *testing.T) {
	s, store, _ := newTestServer(t)
	id := seedQuestion(t, store)

	rr := post(t, s, "/api/feedback", feedback.Event{
		Block: "1/2", Type: "reply", Text: "Because the API caps it.", ReplyTo: id,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ReplyTo != id || got[1].Type != "reply" {
		t.Fatalf("disk state wrong: %+v", got)
	}
}

// A reply naming nothing is refused, and refused *before* anything is written:
// a log that accrues answers to notes it does not hold is a log nobody can
// replay.
func TestReplyToAnUnknownNoteIsRefusedAndAppendsNothing(t *testing.T) {
	s, store, _ := newTestServer(t)
	seedQuestion(t, store)

	rr := post(t, s, "/api/feedback", feedback.Event{
		Block: "1/2", Type: "reply", Text: "Because.", ReplyTo: "000000000000",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown note") {
		t.Errorf("the error should say what did not resolve, got %q", rr.Body.String())
	}
	got, _ := store.Load()
	if len(got) != 1 {
		t.Fatalf("a refused reply must not be appended: %+v", got)
	}
}

// A reply is not review vocabulary, so `builtins: false` cannot turn it off —
// but it still has to say something, because an empty answer answers nothing.
func TestEmptyReplyIsRefusedEvenWithNoBuiltins(t *testing.T) {
	cfg := config(t, "builtins: false\nactions:\n  - type: blocker\n    label: Blocker\n")
	s, store := framed(t, cfg)
	e := feedback.Event{Block: "1/2", Type: "blocker", Author: "berkay", Ts: "2026-07-03T10:00:00Z"}
	if err := store.Append(e); err != nil {
		t.Fatal(err)
	}
	id := feedback.NoteID(e)

	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: "reply", ReplyTo: id}); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty reply: code=%d, want 400", rr.Code)
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: "reply", Text: "Cap comes from the API.", ReplyTo: id}); rr.Code != http.StatusCreated {
		t.Fatalf("reply under builtins:false: code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// A readonly pattern added after a thread opened must not strand it: the
// reviewer asked a question about that block and the answer belongs with it.
// Root vocabulary on the same block is still refused.
func TestReplyIsAllowedOnAReadonlyBlock(t *testing.T) {
	cfg := config(t, "readonly:\n  - \"2\"\n")
	s, store := framed(t, cfg)
	e := feedback.Event{Block: "2/1", Type: "question", Text: "Is this still true?", Author: "berkay", Ts: "2026-07-03T10:00:00Z"}
	if err := store.Append(e); err != nil {
		t.Fatal(err)
	}

	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "2/1", Type: "comment", Text: "no"}); rr.Code != http.StatusForbidden {
		t.Fatalf("comment on a read-only block: code=%d, want 403", rr.Code)
	}
	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "2/1", Type: "reply", Text: "Still true.", ReplyTo: feedback.NoteID(e)}); rr.Code != http.StatusCreated {
		t.Fatalf("reply on a read-only block: code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// Protocol names are the server's, not the config's: a review that redefined
// `reply` would make the same word mean two things in one log.
func TestConfigCannotRedefineAProtocolType(t *testing.T) {
	path := writeConfig(t, "actions:\n  - type: reply\n    label: Reply\n")
	if _, err := review.Load(path); err == nil {
		t.Fatal("a config defining `reply` should be refused")
	}
	path = writeConfig(t, "actions:\n  - type: review_done\n    label: Done\n")
	if _, err := review.Load(path); err == nil {
		t.Fatal("a config defining `review_done` should be refused")
	}
}

// writeConfig drops a review config on disk and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "review.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The revision-loop events go through the same gate as a reply: they name a
// note, and the note has to be one this document's log holds.
func TestProgressEventsAreGatedLikeReplies(t *testing.T) {
	s, store, _ := newTestServer(t)
	id := seedQuestion(t, store)

	for _, typ := range []string{"addressed", "confirm", "reopen"} {
		rr := post(t, s, "/api/feedback", feedback.Event{
			Block: "1/2", Type: typ, Text: "did the thing", ReplyTo: id,
		})
		if rr.Code != http.StatusCreated {
			t.Errorf("%s: code=%d body=%s", typ, rr.Code, rr.Body.String())
		}
		if rr := post(t, s, "/api/feedback", feedback.Event{
			Block: "1/2", Type: typ, Text: "did the thing", ReplyTo: "000000000000",
		}); rr.Code != http.StatusBadRequest {
			t.Errorf("%s naming an unknown note: code=%d, want 400", typ, rr.Code)
		}
		if rr := post(t, s, "/api/feedback", feedback.Event{
			Block: "1/2", Type: typ, Text: "did the thing",
		}); rr.Code != http.StatusBadRequest {
			t.Errorf("%s naming no note at all: code=%d, want 400", typ, rr.Code)
		}
	}
}

// A confirm is one tap. An `addressed` is not: the reviewer is being asked to
// re-check a block, so they are owed a sentence saying what changed.
func TestAddressedNeedsWordsButAVerdictDoesNot(t *testing.T) {
	s, store, _ := newTestServer(t)
	id := seedQuestion(t, store)

	if rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: "addressed", ReplyTo: id}); rr.Code != http.StatusBadRequest {
		t.Errorf("a wordless `addressed` is the assertion this replaces: code=%d, want 400", rr.Code)
	}
	for _, typ := range []string{"confirm", "reopen"} {
		if rr := post(t, s, "/api/feedback", feedback.Event{Block: "1/2", Type: typ, ReplyTo: id}); rr.Code != http.StatusCreated {
			t.Errorf("%s should be one tap: code=%d body=%s", typ, rr.Code, rr.Body.String())
		}
	}
}

// require_verdict asks the *reviewer* to look at every block. An agent's
// reply, or its claim to have addressed a note, must not satisfy it on their
// behalf — that would close the handover with blocks nobody read.
func TestProtocolEventsDoNotSatisfyRequireVerdict(t *testing.T) {
	cfg := config(t, "require_verdict: true\n")
	s, store := framed(t, cfg)
	// Four blocks; the reviewer judges three and asks about the fourth.
	for _, b := range []string{"1/1", "1/2", "2/1"} {
		if rr := post(t, s, "/api/feedback", feedback.Event{Block: b, Type: "approve"}); rr.Code != http.StatusCreated {
			t.Fatalf("seeding %s: %d", b, rr.Code)
		}
	}
	q := feedback.Event{Block: "2/2", Type: "question", Text: "and this?", Author: "berkay", Ts: "2026-07-03T10:00:00Z"}
	if err := store.Append(q); err != nil {
		t.Fatal(err)
	}
	// The question answers 2/2, so Done is available...
	if rr := post(t, s, "/api/feedback", feedback.Event{Type: "review_done"}); rr.Code != http.StatusCreated {
		t.Fatalf("every block has something said about it: code=%d body=%s", rr.Code, rr.Body.String())
	}

	// ...but on a fresh document where the agent has only *replied*, it is not.
	s2, store2 := framed(t, config(t, "require_verdict: true\n"))
	seed := feedback.Event{Block: "1/1", Type: "question", Text: "?", Author: "berkay", Ts: "2026-07-03T10:00:00Z"}
	if err := store2.Append(seed); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"reply", "addressed"} {
		if rr := post(t, s2, "/api/feedback", feedback.Event{
			Block: "1/1", Type: typ, Text: "answered", ReplyTo: feedback.NoteID(seed),
		}); rr.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", typ, rr.Code, rr.Body.String())
		}
	}
	rr := post(t, s2, "/api/feedback", feedback.Event{Type: "review_done"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("code=%d, want 409 — three blocks still have nothing said about them", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "3 still to go") {
		t.Errorf("the agent's own events must not count toward the gate: %q", rr.Body.String())
	}
}
