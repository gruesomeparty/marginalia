package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
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

// The acceptance criterion of #48, driven through the CLI: note → edit →
// record → the listing shows it addressed, and it leaves the work queue.
func TestAddressedRecordsTheClaimAndItChecksOut(t *testing.T) {
	doc := seed(t, replyDoc, question("Why 500?"))
	id := feedback.NoteID(mustLoad(t, doc)[0])

	// The agent edits the document. Marginalia never does this itself.
	if err := os.WriteFile(doc, []byte("# Spec\n\nCapped at 2000 now.\n\n## Retry\n\nThree attempts.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "addressed", doc, "--to", id, "--text", "Raised the cap to 2000.", "--author", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "marked addressed") {
		t.Errorf("unhelpful confirmation: %q", out)
	}
	events := mustLoad(t, doc)
	if len(events) != 2 || events[1].Type != "addressed" || events[1].ReplyTo != id {
		t.Fatalf("the claim does not name the note: %+v", events)
	}
	// It records the hash the block had when the note was written — that is
	// what the re-render checks the claim against.
	if events[1].Hash != events[0].Hash || events[1].Hash == "" {
		t.Errorf("the pre-edit hash was not recorded: %q vs %q", events[1].Hash, events[0].Hash)
	}

	var threads []thread
	jsonOut, err := runCmd(t, "reply", doc, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &threads); err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || threads[0].Status != "addressed" {
		t.Fatalf("status = %+v", threads)
	}
	if threads[0].Unclaimed {
		t.Error("the block changed, so the claim is borne out")
	}
}

// Claiming without editing is recorded and flagged, in the listing an agent
// reads as well as in the page a human reads.
func TestAddressedWithoutEditingIsFlagged(t *testing.T) {
	doc := seed(t, replyDoc, question("Why 500?"))
	id := feedback.NoteID(mustLoad(t, doc)[0])
	if _, err := runCmd(t, "addressed", doc, "--to", id, "--text", "Raised the cap."); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "reply", doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "has not changed since") {
		t.Errorf("an unsubstantiated claim should say so: %q", out)
	}
}

func TestAddressedNeedsToSayWhatChanged(t *testing.T) {
	doc := seed(t, replyDoc, question("Why 500?"))
	id := feedback.NoteID(mustLoad(t, doc)[0])
	if _, err := runCmd(t, "addressed", doc, "--to", id, "--text", "  "); err == nil {
		t.Fatal("a wordless claim should be refused")
	}
	if len(mustLoad(t, doc)) != 1 {
		t.Error("a refused claim must append nothing")
	}
}

// The work queue: what still wants doing comes first, and a settled note
// drops out of it. This is what makes a second round short.
func TestTheListingLeadsWithWhatStillWantsDoing(t *testing.T) {
	doc := seed(t, replyDoc,
		question("Why 500?"),
		// 1.1/2, not 2/2: `## Retry` is a subsection of `# Spec`, so its
		// paragraph is 1.1/2. A note on a block the document does not have
		// would be orphaned, and would test the orphan path by accident.
		feedback.Event{Block: "1.1/2", Hash: "auto", Type: "comment", Text: "tighten this", Author: "berkay", Ts: "2026-07-03T10:01:00Z"},
	)
	events := mustLoad(t, doc)
	first := feedback.NoteID(events[0])

	// Address and settle the first; the second is untouched.
	if _, err := runCmd(t, "addressed", doc, "--to", first, "--text", "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "reply", doc, "--to", first, "--text", "x"); err != nil {
		t.Fatal(err)
	}
	var threads []thread
	jsonOut, _ := runCmd(t, "reply", doc, "--json")
	if err := json.Unmarshal([]byte(jsonOut), &threads); err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("expected two notes, got %d", len(threads))
	}
	for _, th := range threads {
		if th.Ts == "" || th.Block == "" {
			t.Fatalf("malformed thread: %+v", th)
		}
	}
	// The untouched comment leads; the addressed question follows.
	if threads[0].Text != "tighten this" || threads[0].Status != "outstanding" {
		t.Errorf("the outstanding note should lead: %+v", threads[0])
	}
	if threads[1].Status != "addressed" {
		t.Errorf("the addressed note should follow: %+v", threads[1])
	}
}

// Issue #67, through the CLI an agent actually reads: a claim on a sentence
// note is judged by that sentence, and the listing says which sentence.
func TestAddressedOnASentenceIsJudgedByThatSentence(t *testing.T) {
	doc := seed(t, replyDoc)
	parsed, err := document.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, b := range parsed.Blocks {
		if b.ID == "1/2" {
			text = b.PlainText
		}
	}
	note := feedback.Event{
		Block: "1/2", Hash: "original", Type: feedback.TypeComment, Text: "no backoff is wrong",
		Author: "berkay", Ts: "2026-07-03T10:00:00Z",
		Sub: feedback.MakeSub(text, "Capped at 500 for now."),
	}
	if note.Sub == nil {
		t.Fatalf("quote not in %q", text)
	}
	if err := feedback.NewStore(doc).Append(note); err != nil {
		t.Fatal(err)
	}

	// The agent edits a different sentence and claims the note is addressed.
	if err := os.WriteFile(doc, []byte("# Spec\n\nCapped at 500 for now. And a new sentence.\n\n## Retry\n\nThree attempts.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "addressed", doc, "--to", feedback.NoteID(note), "--text", "done"); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "reply", doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the sentence has not changed since") {
		t.Errorf("an unsubstantiated sentence claim must say so, and say sentence: %q", out)
	}
	if !strings.Contains(out, "Capped at 500 for now.") {
		t.Errorf("the listing should name the sentence the note is about: %q", out)
	}
}
