package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
)

// ask puts one question in a document's log, the way a reviewer would, and
// returns the id an answer names it by.
//
// It carries the block's real hash, because the page always writes one and
// because the hash is what makes an `addressed` claim checkable — a fixture
// without it would quietly test the "cannot be proven" path instead.
func ask(t *testing.T, doc, text string) string {
	t.Helper()
	parsed, err := document.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	var hash, quote string
	for _, b := range parsed.Blocks {
		if b.ID == "1/2" {
			hash, quote = b.Hash, b.Quote
		}
	}
	if hash == "" {
		t.Fatalf("fixture has no block 1/2 to hang a question on")
	}
	e := feedback.Event{
		Doc: doc, Block: "1/2", Quote: quote, Hash: hash, Type: feedback.TypeQuestion,
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

	var out ReplyOut
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

// The acceptance criterion of issue #48 driven the way an agent would: read
// the note, change the document, record the claim, and see the reviewer's
// queue reflect it.
func TestMarkAddressedClosesTheRevisionLoop(t *testing.T) {
	root := t.TempDir()
	doc := writeDoc(t, root, "spec.md", specDoc)
	id := ask(t, doc, "Why 500?")
	cs, _ := serveMCP(t, root)

	// The agent edits the document — Marginalia never does this itself.
	writeDoc(t, root, "spec.md", "# Spec\n\nCapped at 2000 now.\n\n## Retry\n\nThree attempts, no backoff.\n")

	var out ReplyOut
	if err := call(t, cs, "mark_addressed", map[string]any{
		"doc": "spec.md", "note_id": id, "text": "Raised the cap to 2000.",
	}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ReplyTo != id || out.Answered != feedback.TypeQuestion {
		t.Fatalf("the claim does not name what it is about: %+v", out)
	}

	var status statusOut
	if err := call(t, cs, "review_status", map[string]any{"paths": []string{"spec.md"}}, &status); err != nil {
		t.Fatal(err)
	}
	d := status.Docs[0]
	if d.Addressed != 1 || d.Outstanding != 0 {
		t.Fatalf("addressed = %d, outstanding = %d; want 1 and 0", d.Addressed, d.Outstanding)
	}
	n := d.States[0].Current
	if n.Status != feedback.StatusAddressed || len(n.Progress) != 1 {
		t.Fatalf("status = %q, progress = %d", n.Status, len(n.Progress))
	}
	if n.Unclaimed {
		t.Error("the block really did change, so the claim is borne out")
	}
	// The note itself is untouched: the log is append-only and the question
	// still reads as the question.
	if n.Event.Type != feedback.TypeQuestion || n.Event.Text != "Why 500?" {
		t.Errorf("the note was altered: %+v", n.Event)
	}
}

// A claim the document does not bear out is recorded and reported as such.
// The agent is not stopped — it may have edited a block elsewhere — but the
// reviewer is told.
func TestMarkAddressedWithoutEditingSaysSo(t *testing.T) {
	root := t.TempDir()
	doc := writeDoc(t, root, "spec.md", specDoc)
	id := ask(t, doc, "Why 500?")
	cs, _ := serveMCP(t, root)

	if err := call(t, cs, "mark_addressed", map[string]any{
		"doc": "spec.md", "note_id": id, "text": "Raised the cap.",
	}, nil); err != nil {
		t.Fatal(err)
	}
	var status statusOut
	if err := call(t, cs, "review_status", map[string]any{"paths": []string{"spec.md"}}, &status); err != nil {
		t.Fatal(err)
	}
	n := status.Docs[0].States[0].Current
	if n.Status != feedback.StatusAddressed {
		t.Errorf("the claim is still recorded: status = %q", n.Status)
	}
	if !n.Unclaimed {
		t.Error("nothing in the document changed — the reviewer has to be told that")
	}
	_ = doc
}

// An `addressed` with no words is the assertion this feature replaces.
func TestMarkAddressedNeedsToSayWhatChanged(t *testing.T) {
	root := t.TempDir()
	doc := writeDoc(t, root, "spec.md", specDoc)
	id := ask(t, doc, "Why 500?")
	cs, _ := serveMCP(t, root)

	if err := call(t, cs, "mark_addressed", map[string]any{
		"doc": "spec.md", "note_id": id, "text": "  ",
	}, nil); err == nil {
		t.Fatal("should have been refused")
	}
	if got := mustLoadN(t, doc); got != 1 {
		t.Errorf("a refused claim must append nothing, log has %d", got)
	}
}

func mustLoadN(t *testing.T, doc string) int {
	t.Helper()
	events, err := feedback.NewStore(doc).Load()
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

// The reads report done by the review's completion rule (#53): with two
// reviewers named and complete_when: all, one Done is half of it.
func TestReadsFollowTheCompletionRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte("# Spec\n\nText.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := TargetOf(path)
	target.Completion = review.Completion{Rule: review.CompleteAll, Reviewers: []string{"ana", "bo"}}
	if err := target.Store.Append(feedback.Event{Type: feedback.TypeReviewDone, Author: "ana"}); err != nil {
		t.Fatal(err)
	}
	ev, err := EventsSince(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Done || ev.DoneBy != 1 || ev.DoneNeeded != 2 {
		t.Errorf("feedback_since: done %v, %d of %d", ev.Done, ev.DoneBy, ev.DoneNeeded)
	}
	st, err := StatusOf(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.Done || st.DoneBy != 1 || st.DoneNeeded != 2 {
		t.Errorf("review_status: done %v, %d of %d", st.Done, st.DoneBy, st.DoneNeeded)
	}
	if err := target.Store.Append(feedback.Event{Type: feedback.TypeReviewDone, Author: "bo"}); err != nil {
		t.Fatal(err)
	}
	if ev, _ := EventsSince(target, 0); !ev.Done {
		t.Error("both reviewers done, the read still says not done")
	}
}

func TestNewestSessionsRuleWins(t *testing.T) {
	s := &Server{sessions: map[string]*live{}}
	all := review.Completion{Rule: review.CompleteAll}
	for i, c := range []review.Completion{{Rule: review.CompleteAny}, all} {
		s.remember(&live{id: fmt.Sprint(i), docs: []string{"/d/spec.md"}, completion: c})
	}
	for range 20 { // map order varies; the answer must not
		if got := s.targetsOf([]string{"/d/spec.md"})[0].Completion; got.Rule != all.Rule {
			t.Fatalf("rule %q, want the newest session's %q", got.Rule, all.Rule)
		}
	}
}

// review_document hands its config's rule to the stdio reads: by path, one
// of two reviewers' Done must not read as done.
func TestReviewDocumentHandsItsRuleToTheReads(t *testing.T) {
	root := t.TempDir()
	path := writeDoc(t, root, "spec.md", specDoc)
	cfg := writeDoc(t, root, "review.yaml", "complete_when: all\nreviewers: [ana, bo]\n")
	cs, _ := serveMCP(t, root)
	if err := call(t, cs, "review_document", map[string]any{"paths": []string{"spec.md"}, "config": cfg}, nil); err != nil {
		t.Fatal(err)
	}
	if err := feedback.NewStore(path).Append(feedback.Event{Type: feedback.TypeReviewDone, Author: "ana"}); err != nil {
		t.Fatal(err)
	}
	var st statusOut
	if err := call(t, cs, "review_status", map[string]any{"paths": []string{"spec.md"}}, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Docs) != 1 || st.Docs[0].Done || st.Docs[0].DoneBy != 1 || st.Docs[0].DoneNeeded != 2 {
		t.Errorf("review_status after one of two Dones: %+v", st.Docs)
	}
	var fb feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"paths": []string{"spec.md"}}, &fb); err != nil {
		t.Fatal(err)
	}
	if len(fb.Docs) != 1 || fb.Docs[0].Done {
		t.Errorf("feedback_since after one of two Dones: %+v", fb.Docs)
	}
}

// review_status names the blocks that moved since the reviewer's Done: a
// stdio read of the file on disk, measured from the parse the live session
// recorded at that Done.
func TestReviewStatusNamesChangedBlocks(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "spec.md", specDoc)
	cs, _ := serveMCP(t, root)
	var started reviewDocumentOut
	if err := call(t, cs, "review_document", map[string]any{"paths": []string{"spec.md"}}, &started); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(feedback.Event{Type: feedback.TypeReviewDone})
	res, err := http.Post(started.URL+"/api/feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("done: %d", res.StatusCode)
	}
	writeDoc(t, root, "spec.md", strings.Replace(specDoc, "Three attempts", "Five attempts", 1))
	var st statusOut
	if err := call(t, cs, "review_status", map[string]any{"paths": []string{"spec.md"}}, &st); err != nil {
		t.Fatal(err)
	}
	if c := st.Docs[0].Changed; len(c.Edited) != 1 || len(c.Added)+len(c.Removed) != 0 {
		t.Errorf("changed_blocks %+v, want the one edited block", c)
	}
}
