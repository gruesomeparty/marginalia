package feedback

import "testing"

// note builds a reviewer's note on block 1/2 with the given hash.
func noteOn(text, hash, ts string) Event {
	return Event{Block: "1/2", Quote: "Capped at 500.", Hash: hash, Type: TypeQuestion, Text: text, Author: "berkay", Ts: ts}
}

func progress(typ, text, hash, to, ts string) Event {
	return Event{Block: "1/2", Hash: hash, Type: typ, Text: text, Author: "agent", Ts: ts, ReplyTo: to}
}

// only returns the single note materialized for block 1/2.
func only(t *testing.T, res Resolution) Note {
	t.Helper()
	for _, st := range res.States {
		if st.Block == "1/2" {
			if len(st.History) != 1 {
				t.Fatalf("expected one root note on 1/2, got %d", len(st.History))
			}
			return st.History[0]
		}
	}
	t.Fatalf("no state for 1/2 in %+v", res.States)
	return Note{}
}

// Silence is not resolution: a note nobody touched is outstanding, and stays
// outstanding however much the document moves around it.
func TestANoteNobodyAddressedStaysOutstanding(t *testing.T) {
	q := noteOn("Why 500?", "aaaa", "2026-07-03T10:00:00Z")
	// The block was edited — repeatedly — but for unrelated reasons.
	res := Materialize([]Event{q}, map[string]string{"1/2": "zzzz"}, []string{"1/2"})
	n := only(t, res)
	if n.Status != StatusOutstanding {
		t.Errorf("status = %q, want outstanding", n.Status)
	}
	if !n.Stale {
		t.Error("the block did change, so the note is stale — that is a separate axis and must still be reported")
	}
	if res.Outstanding != 1 {
		t.Errorf("outstanding = %d, want 1", res.Outstanding)
	}
}

// The acceptance criterion of issue #48, in order: addressed → confirmed, and
// the note leaves the queue.
func TestAddressedThenConfirmedLeavesTheQueue(t *testing.T) {
	q := noteOn("Why 500?", "aaaa", "2026-07-03T10:00:00Z")
	id := NoteID(q)
	a := progress(TypeAddressed, "Raised the cap to 2000.", "aaaa", id, "2026-07-03T11:00:00Z")
	hashes := map[string]string{"1/2": "bbbb"} // the block really did change

	res := Materialize([]Event{q, a}, hashes, []string{"1/2"})
	n := only(t, res)
	if n.Status != StatusAddressed || len(n.Progress) != 1 {
		t.Fatalf("status = %q, progress = %d; want addressed with one entry", n.Status, len(n.Progress))
	}
	if n.Unclaimed {
		t.Error("the block changed, so the claim is borne out")
	}
	if res.Addressed != 1 || res.Outstanding != 0 {
		t.Errorf("addressed = %d, outstanding = %d; want 1 and 0", res.Addressed, res.Outstanding)
	}

	c := progress(TypeConfirm, "", "", id, "2026-07-03T12:00:00Z")
	res = Materialize([]Event{q, a, c}, hashes, []string{"1/2"})
	n = only(t, res)
	if n.Status != StatusConfirmed {
		t.Errorf("status = %q, want confirmed", n.Status)
	}
	if res.Confirmed != 1 || res.Outstanding != 0 || res.Addressed != 0 {
		t.Errorf("counts wrong: %+v", res)
	}
}

// Reopening returns the note to the queue. The log is append-only, so this is
// the latest progress event winning — nothing is rewritten.
func TestReopenReturnsANoteToTheQueue(t *testing.T) {
	q := noteOn("Why 500?", "aaaa", "2026-07-03T10:00:00Z")
	id := NoteID(q)
	events := []Event{
		q,
		progress(TypeAddressed, "Raised the cap.", "aaaa", id, "2026-07-03T11:00:00Z"),
		progress(TypeConfirm, "", "", id, "2026-07-03T12:00:00Z"),
		progress(TypeReopen, "Still says 500 two paragraphs down.", "", id, "2026-07-03T13:00:00Z"),
	}
	res := Materialize(events, map[string]string{"1/2": "bbbb"}, []string{"1/2"})
	n := only(t, res)
	if n.Status != StatusReopened {
		t.Fatalf("status = %q, want reopened", n.Status)
	}
	if res.Outstanding != 1 || res.Confirmed != 0 {
		t.Errorf("a reopened note is outstanding again: %+v", res)
	}
	if len(n.Progress) != 3 {
		t.Errorf("the whole loop should be readable, got %d entries", len(n.Progress))
	}
	// And it can be addressed again — the loop is a loop.
	events = append(events, progress(TypeAddressed, "Fixed the second one too.", "bbbb", id, "2026-07-03T14:00:00Z"))
	if got := only(t, Materialize(events, map[string]string{"1/2": "cccc"}, []string{"1/2"})).Status; got != StatusAddressed {
		t.Errorf("status = %q, want addressed again", got)
	}
}

// "Checkable rather than asserted": an addressed claim whose block never moved
// is shown, and shown as unsubstantiated.
func TestAnAddressedClaimTheDocumentDoesNotBearOut(t *testing.T) {
	q := noteOn("Why 500?", "aaaa", "2026-07-03T10:00:00Z")
	id := NoteID(q)
	a := progress(TypeAddressed, "Raised the cap.", "aaaa", id, "2026-07-03T11:00:00Z")

	// The block still hashes to what it did before the claimed edit.
	n := only(t, Materialize([]Event{q, a}, map[string]string{"1/2": "aaaa"}, []string{"1/2"}))
	if n.Status != StatusAddressed {
		t.Errorf("the claim is still reported: status = %q", n.Status)
	}
	if !n.Unclaimed {
		t.Error("nothing changed, so the claim is not borne out and must say so")
	}

	// A claim with no hash cannot be checked — not the same as being false.
	noHash := progress(TypeAddressed, "Raised the cap.", "", id, "2026-07-03T11:00:00Z")
	if only(t, Materialize([]Event{q, noHash}, map[string]string{"1/2": "aaaa"}, []string{"1/2"})).Unclaimed {
		t.Error("a claim with no hash cannot be disproven, so it must not be marked as disproven")
	}
}

// An approve asks for nothing. Counting it as outstanding would make a
// fully-approved document read as a full second-round queue.
func TestAnApproveIsNotOutstandingWork(t *testing.T) {
	ok := Event{Block: "1/1", Type: TypeApprove, Author: "berkay", Ts: "2026-07-03T10:00:00Z"}
	q := noteOn("Why 500?", "aaaa", "2026-07-03T10:00:00Z")
	res := Materialize([]Event{ok, q}, map[string]string{"1/1": "x", "1/2": "aaaa"}, []string{"1/1", "1/2"})
	if res.Outstanding != 1 {
		t.Errorf("outstanding = %d, want 1 (the question, not the approval)", res.Outstanding)
	}
}

// Status and replies are different things that arrive the same way. An answer
// must not read as progress, and progress must not inflate what was said.
func TestStatusAndAnswersDoNotContaminateEachOther(t *testing.T) {
	q := noteOn("Why 500?", "aaaa", "2026-07-03T10:00:00Z")
	id := NoteID(q)
	events := []Event{
		q,
		{Block: "1/2", Type: TypeReply, Text: "The API caps it.", Author: "agent", Ts: "2026-07-03T11:00:00Z", ReplyTo: id},
		progress(TypeAddressed, "Documented the cap.", "aaaa", id, "2026-07-03T12:00:00Z"),
	}
	res := Materialize(events, map[string]string{"1/2": "bbbb"}, []string{"1/2"})
	n := only(t, res)
	if len(n.Replies) != 1 || n.Replies[0].Event.Type != TypeReply {
		t.Errorf("the answer belongs in the thread: %+v", n.Replies)
	}
	if len(n.Progress) != 1 || n.Progress[0].Event.Type != TypeAddressed {
		t.Errorf("the status belongs in the revision loop: %+v", n.Progress)
	}
	if res.Replies != 1 {
		t.Errorf("replies = %d, want 1 — a status is not something said", res.Replies)
	}
	if res.Comments != 1 {
		t.Errorf("comments = %d, want 1 — neither a reply nor a status is a comment", res.Comments)
	}
}

// A log written before any of this parses and reads exactly as it did.
func TestLogsWrittenBeforeReReviewAreUnchanged(t *testing.T) {
	events := []Event{
		{Block: "1/1", Type: TypeComment, Text: "tighten this", Author: "berkay", Ts: "2026-07-01T10:00:00Z"},
		{Block: "1/2", Type: TypeApprove, Author: "berkay", Ts: "2026-07-01T10:01:00Z"},
	}
	res := Materialize(events, map[string]string{"1/1": "a", "1/2": "b"}, []string{"1/1", "1/2"})
	if res.Comments != 2 || res.Addressed != 0 || res.Confirmed != 0 {
		t.Fatalf("an old log should materialize as it always did: %+v", res)
	}
	for _, st := range res.States {
		for _, n := range st.History {
			if n.Status != StatusOutstanding || len(n.Progress) != 0 {
				t.Errorf("%s: status = %q, progress = %d", st.Block, n.Status, len(n.Progress))
			}
		}
	}
	if res.Outstanding != 1 {
		t.Errorf("outstanding = %d, want 1 (the comment; the approval asks nothing)", res.Outstanding)
	}
}

// The block a note was about can be deleted outright. The note is already
// surfaced as orphaned; its status has to survive that, since "I addressed it
// by removing the paragraph" is a real answer.
func TestStatusSurvivesAnOrphanedBlock(t *testing.T) {
	q := noteOn("Why 500?", "aaaa", "2026-07-03T10:00:00Z")
	id := NoteID(q)
	a := progress(TypeAddressed, "Cut the paragraph entirely.", "aaaa", id, "2026-07-03T11:00:00Z")
	res := Materialize([]Event{q, a}, map[string]string{}, nil)
	if len(res.States) != 1 || !res.States[0].Orphaned {
		t.Fatalf("the note should be orphaned: %+v", res.States)
	}
	n := res.States[0].History[0]
	if n.Status != StatusAddressed {
		t.Errorf("status = %q, want addressed", n.Status)
	}
	if n.Unclaimed {
		t.Error("the block is gone, which is a change — not an unsubstantiated claim")
	}
}
