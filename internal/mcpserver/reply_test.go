package mcpserver

import (
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// ask puts one question in a document's log, the way a reviewer would, and
// returns the id an answer names it by.
func ask(t *testing.T, doc, text string) string {
	t.Helper()
	e := feedback.Event{
		Doc: doc, Block: "1/2", Quote: "Capped at 500 for now.", Type: feedback.TypeQuestion,
		Text: text, Author: "berkay", Ts: "2026-07-03T10:00:00Z",
	}
	if err := feedback.NewStore(doc).Append(e); err != nil {
		t.Fatal(err)
	}
	return feedback.NoteID(e)
}

// The acceptance criterion of issue #47, from the agent's side: a question
// read out of the log can be answered back into it, and the answer hangs
// under the question rather than becoming a note of its own.
func TestReplyToNoteAnswersInPlace(t *testing.T) {
	root := t.TempDir()
	doc := writeDoc(t, root, "spec.md", specDoc)
	id := ask(t, doc, "Why 500?")
	cs, _ := serveMCP(t, root)

	var out replyOut
	if err := call(t, cs, "reply_to_note", map[string]any{
		"doc": "spec.md", "note_id": id, "text": "The upstream API caps it.",
	}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ReplyTo != id || out.Block != "1/2" || out.Answered != feedback.TypeQuestion || out.ID == "" {
		t.Fatalf("reply did not name what it answered: %+v", out)
	}

	var status statusOut
	if err := call(t, cs, "review_status", map[string]any{"paths": []string{"spec.md"}}, &status); err != nil {
		t.Fatal(err)
	}
	d := status.Docs[0]
	if d.Comments != 1 || d.Replies != 1 {
		t.Fatalf("an answer must not count as a comment: %d comments, %d replies", d.Comments, d.Replies)
	}
	if len(d.States) != 1 || len(d.States[0].History) != 1 {
		t.Fatalf("the reply became a note of its own: %+v", d.States)
	}
	note := d.States[0].Current
	if note.ID != id || len(note.Replies) != 1 || note.Replies[0].Event.Text != "The upstream API caps it." {
		t.Fatalf("the answer is not under the question: %+v", note)
	}
	if d.States[0].Current.Event.Type != feedback.TypeQuestion {
		t.Error("answering a question must not change what the block's current state is")
	}
}

// feedback_since is where an agent reads the human's words; it has to hand
// back the id too, or answering means going and looking somewhere else.
func TestFeedbackSinceCarriesTheIdToAnswer(t *testing.T) {
	root := t.TempDir()
	doc := writeDoc(t, root, "spec.md", specDoc)
	id := ask(t, doc, "Why 500?")
	cs, _ := serveMCP(t, root)

	var seen feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"paths": []string{"spec.md"}}, &seen); err != nil {
		t.Fatal(err)
	}
	if len(seen.Docs[0].Events) != 1 || seen.Docs[0].Events[0].ID != id {
		t.Fatalf("no id to reply to: %+v", seen.Docs[0].Events)
	}
	if err := call(t, cs, "reply_to_note", map[string]any{
		"doc": "spec.md", "note_id": seen.Docs[0].Events[0].ID, "text": "Because the API caps it.",
	}, nil); err != nil {
		t.Fatalf("the id feedback_since reported did not resolve: %v", err)
	}
}

// An agent must not be able to answer a note nobody wrote — and must not be
// able to put anything but a reply in the log. Both are the same rule: the
// log is the reviewer's, and the one thing an agent adds to it names what it
// answers.
func TestReplyToNoteRefusesWhatItCannotAnchor(t *testing.T) {
	root := t.TempDir()
	doc := writeDoc(t, root, "spec.md", specDoc)
	id := ask(t, doc, "Why 500?")
	cs, _ := serveMCP(t, root)

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"unknown note", map[string]any{"doc": "spec.md", "note_id": "000000000000", "text": "hi"}, "no note"},
		{"no note named", map[string]any{"doc": "spec.md", "text": "hi"}, "note_id"},
		{"nothing to say", map[string]any{"doc": "spec.md", "note_id": id, "text": "   "}, "needs something to say"},
		{"outside the root", map[string]any{"doc": "../elsewhere.md", "note_id": id, "text": "hi"}, ""},
	} {
		err := call(t, cs, "reply_to_note", tc.args, nil)
		if err == nil {
			t.Errorf("%s: should have been refused", tc.name)
			continue
		}
		if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error should say why, got %q", tc.name, err)
		}
	}
	events, err := feedback.NewStore(doc).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("a refused reply must append nothing: %+v", events)
	}
}

// The type is the tool's, not the caller's: there is no argument that makes
// reply_to_note write an approve, a comment or a review_done.
func TestReplyToNoteOnlyEverWritesAReply(t *testing.T) {
	root := t.TempDir()
	doc := writeDoc(t, root, "spec.md", specDoc)
	id := ask(t, doc, "Why 500?")
	cs, _ := serveMCP(t, root)

	// There is no such input, and the schema says so rather than ignoring it:
	// a caller that thought it was choosing a type should hear that it was not.
	if err := call(t, cs, "reply_to_note", map[string]any{
		"doc": "spec.md", "note_id": id, "text": "Answered.", "type": "approve",
	}, nil); err == nil {
		t.Fatal("reply_to_note should not accept a type")
	}
	if err := call(t, cs, "reply_to_note", map[string]any{
		"doc": "spec.md", "note_id": id, "text": "Answered.", "author": "agent",
	}, nil); err != nil {
		t.Fatal(err)
	}
	events, err := feedback.NewStore(doc).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != "reply" || events[1].ReplyTo != id {
		t.Fatalf("the log took something other than a reply: %+v", events)
	}
}
