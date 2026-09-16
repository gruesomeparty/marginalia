package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

const replyDoc = "# Spec\n\nCapped at 500 for now.\n\n## Retry\n\nThree attempts.\n"

func question(text string) feedback.Event {
	return feedback.Event{
		Block: "1/2", Quote: "Capped at 500 for now.", Hash: "auto",
		Type: feedback.TypeQuestion, Text: text, Author: "berkay", Ts: "2026-07-03T10:00:00Z",
	}
}

// With no --to, `reply` is the way to find out what to answer.
func TestReplyListsNotesWithTheIdToAnswer(t *testing.T) {
	doc := seed(t, replyDoc, question("Why 500?"))
	out, err := runCmd(t, "reply", doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Why 500?") || !strings.Contains(out, "question") {
		t.Fatalf("the listing should show the note: %q", out)
	}
	var threads []thread
	jsonOut, err := runCmd(t, "reply", doc, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &threads); err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].ID == "" {
		t.Fatalf("no id to answer: %+v", threads)
	}
	if !strings.Contains(out, threads[0].ID) {
		t.Errorf("the human-readable listing should print the id too: %q", out)
	}
}

// The acceptance criterion, from the CLI: an answer lands in the log pointing
// at the question, and the question stays the block's current state.
func TestReplyAppendsAnAnswerToTheNamedNote(t *testing.T) {
	doc := seed(t, replyDoc, question("Why 500?"))
	id := feedback.NoteID(mustLoad(t, doc)[0])

	if _, err := runCmd(t, "reply", doc, "--to", id, "--text", "The API caps it.", "--author", "agent"); err != nil {
		t.Fatal(err)
	}
	events := mustLoad(t, doc)
	if len(events) != 2 {
		t.Fatalf("expected the answer to be appended: %+v", events)
	}
	got := events[1]
	if got.Type != "reply" || got.ReplyTo != id || got.Block != "1/2" || got.Author != "agent" {
		t.Fatalf("the answer does not name what it answers: %+v", got)
	}
	// It carries the question's own anchor, so a reader of the raw log knows
	// what it is about without resolving the pointer first.
	if got.Quote != "Capped at 500 for now." || got.Hash == "" {
		t.Errorf("the answer should carry the note's anchor: %+v", got)
	}

	out, err := runCmd(t, "reply", doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "The API caps it.") {
		t.Errorf("the listing should show the answer under the question: %q", out)
	}
}

func TestReplyRefusesWhatItCannotAnchor(t *testing.T) {
	doc := seed(t, replyDoc, question("Why 500?"))
	id := feedback.NoteID(mustLoad(t, doc)[0])
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown note", []string{"reply", doc, "--to", "000000000000", "--text", "hi"}, "no note"},
		{"nothing to say", []string{"reply", doc, "--to", id, "--text", "  "}, "needs something to say"},
	} {
		out, err := runCmd(t, tc.args...)
		if err == nil {
			t.Errorf("%s: should have been refused (%q)", tc.name, out)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error should say why, got %q", tc.name, err)
		}
	}
	if len(mustLoad(t, doc)) != 1 {
		t.Error("a refused reply must append nothing")
	}
}

// An import that carries an answer to a note this log has never seen is
// refused rather than merged: the sender still has the review it answers, and
// this is the last moment the mismatch can be reported to someone who can fix
// it.
func TestImportRefusesAnAnswerToANoteNobodyHolds(t *testing.T) {
	doc := seed(t, replyDoc, question("Why 500?"))
	shared := filepath.Join(t.TempDir(), "review.json")
	body := `{"doc":"` + doc + `","events":[{"block":"1/2","type":"reply","text":"answer","author":"a","ts":"2026-07-03T11:00:00Z","reply_to":"000000000000"}]}`
	if err := os.WriteFile(shared, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "import", shared); err == nil {
		t.Fatal("an answer to nothing should be refused")
	} else if !strings.Contains(err.Error(), "replies to note") {
		t.Errorf("the error should say what did not resolve: %v", err)
	}
	if len(mustLoad(t, doc)) != 1 {
		t.Error("a refused import must append nothing")
	}
}

// A reply whose target arrives in the same file is fine — that is one review
// exported and merged whole — and merging it twice changes nothing.
func TestImportTakesASelfContainedThreadIdempotently(t *testing.T) {
	doc := seed(t, replyDoc)
	q := feedback.Event{Block: "1/2", Type: feedback.TypeQuestion, Text: "Why 500?", Author: "berkay", Ts: "2026-07-03T10:00:00Z"}
	r := feedback.Event{Block: "1/2", Type: "reply", Text: "The API caps it.", Author: "agent", Ts: "2026-07-03T11:00:00Z", ReplyTo: feedback.NoteID(q)}
	raw, err := json.Marshal(map[string]any{"doc": doc, "events": []feedback.Event{q, r}})
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(shared, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "import", shared); err != nil {
		t.Fatal(err)
	}
	if got := mustLoad(t, doc); len(got) != 2 || got[1].ReplyTo == "" {
		t.Fatalf("the thread did not survive the merge: %+v", got)
	}
	if _, err := runCmd(t, "import", shared); err != nil {
		t.Fatal(err)
	}
	if got := mustLoad(t, doc); len(got) != 2 {
		t.Fatalf("importing twice duplicated the thread: %+v", got)
	}
}

func mustLoad(t *testing.T, doc string) []feedback.Event {
	t.Helper()
	events, err := feedback.NewStore(doc).Load()
	if err != nil {
		t.Fatal(err)
	}
	return events
}
