package feedback

import "testing"

func note(block, hash, typ, text, ts string) Event {
	return Event{Block: block, Quote: "quote of " + block, Hash: hash, Type: typ, Text: text, Ts: ts}
}

func stateOf(res Resolution, block string) *State {
	for i := range res.States {
		if res.States[i].Block == block {
			return &res.States[i]
		}
	}
	return nil
}

// The second-pass view: what was said, what still applies, and what the
// document has outrun.
func TestMaterializeFlagsStaleAndOrphaned(t *testing.T) {
	events := []Event{
		note("1/1", "aaa", TypeComment, "still true", "2026-07-03T10:00:00Z"),
		note("1/2", "old", TypeReject, "too low", "2026-07-03T10:05:00Z"),
		note("2/1", "ccc", TypeQuestion, "gone with its section", "2026-07-03T10:10:00Z"),
		{Type: TypeReviewDone, Ts: "2026-07-03T11:00:00Z"},
	}
	hashes := map[string]string{"1/1": "aaa", "1/2": "new"}
	res := Materialize(events, hashes, []string{"1/1", "1/2"})

	if res.Comments != 3 || res.Stale != 1 || res.Orphaned != 1 || !res.Done {
		t.Fatalf("counts = %+v", res)
	}
	if got := stateOf(res, "1/1"); got == nil || got.Stale || got.Orphaned {
		t.Errorf("untouched block = %+v", got)
	}
	if got := stateOf(res, "1/2"); got == nil || !got.Stale || got.Orphaned {
		t.Errorf("edited block should be stale, not orphaned: %+v", got)
	}
	// A note whose block is gone is surfaced, never dropped: the quote is all
	// that is left of what it was about.
	lost := stateOf(res, "2/1")
	if lost == nil || !lost.Orphaned || lost.Stale {
		t.Fatalf("orphaned block = %+v", lost)
	}
	if lost.Current.Event.Quote != "quote of 2/1" {
		t.Errorf("orphan lost its quote: %+v", lost.Current.Event)
	}
	// Document order first, orphans after — they have no place in the text.
	if res.States[len(res.States)-1].Block != "2/1" {
		t.Errorf("orphans should come last: %+v", res.States)
	}
}

func TestMaterializeFollowsDocumentOrder(t *testing.T) {
	events := []Event{
		note("3/1", "c", TypeComment, "third", "2026-07-03T10:00:00Z"),
		note("1/1", "a", TypeComment, "first", "2026-07-03T10:01:00Z"),
		note("2/1", "b", TypeComment, "second", "2026-07-03T10:02:00Z"),
	}
	hashes := map[string]string{"1/1": "a", "2/1": "b", "3/1": "c"}
	res := Materialize(events, hashes, []string{"1/1", "2/1", "3/1"})
	var got []string
	for _, s := range res.States {
		got = append(got, s.Block)
	}
	if len(got) != 3 || got[0] != "1/1" || got[1] != "2/1" || got[2] != "3/1" {
		t.Errorf("order = %v, want document order", got)
	}
}

// Later events on a block override earlier ones, so a fresh note on an edited
// block clears the staleness the old one carried — the history keeps both.
func TestMaterializeCurrentIsLatest(t *testing.T) {
	events := []Event{
		note("1/1", "old", TypeReject, "was rejected", "2026-07-03T10:00:00Z"),
		note("1/1", "new", TypeApprove, "fixed now", "2026-07-03T12:00:00Z"),
	}
	res := Materialize(events, map[string]string{"1/1": "new"}, []string{"1/1"})
	st := stateOf(res, "1/1")
	if st == nil || st.Current.Event.Text != "fixed now" || st.Stale {
		t.Fatalf("state = %+v", st)
	}
	if len(st.History) != 2 || !st.History[0].Stale || st.History[1].Stale {
		t.Errorf("history should keep the stale note and the current one: %+v", st.History)
	}
	if res.Stale != 0 {
		t.Errorf("a block whose latest note is current is not stale: %d", res.Stale)
	}
}

// Feedback written before hashing existed, or against a block that had none,
// is reported as it is rather than guessed at.
func TestMaterializeTreatsMissingHashAsCurrent(t *testing.T) {
	events := []Event{note("1/1", "", TypeComment, "no hash", "2026-07-03T10:00:00Z")}
	res := Materialize(events, map[string]string{"1/1": "aaa"}, []string{"1/1"})
	if st := stateOf(res, "1/1"); st == nil || st.Stale {
		t.Errorf("state = %+v, want not stale", st)
	}
}

func TestMaterializeEmptyLog(t *testing.T) {
	res := Materialize(nil, map[string]string{"1/1": "a"}, []string{"1/1"})
	if len(res.States) != 0 || res.Comments != 0 || res.Done {
		t.Errorf("empty log = %+v", res)
	}
}

// Applying a suggestion changes a document, so the bar is higher than reading
// one: it must be the note that stands, and its hash must prove the block has
// not moved under it.
func TestSuggestionsSplitsByWhatIsProvable(t *testing.T) {
	events := []Event{
		note("1/1", "aaa", TypeSuggestEdit, "fixed wording", "2026-07-03T10:00:00Z"),
		note("1/2", "old", TypeSuggestEdit, "on edited text", "2026-07-03T10:01:00Z"),
		note("1/3", "", TypeSuggestEdit, "no hash to check", "2026-07-03T10:02:00Z"),
		note("1/4", "ddd", TypeSuggestEdit, "then superseded", "2026-07-03T10:03:00Z"),
		note("1/4", "ddd", TypeApprove, "looks fine now", "2026-07-03T10:04:00Z"),
		note("9/9", "zzz", TypeSuggestEdit, "on a deleted block", "2026-07-03T10:05:00Z"),
		note("1/5", "eee", TypeComment, "not a suggestion", "2026-07-03T10:06:00Z"),
		note("1/6", "fff", TypeSuggestEdit, "", "2026-07-03T10:07:00Z"),
	}
	hashes := map[string]string{"1/1": "aaa", "1/2": "new", "1/3": "ccc", "1/4": "ddd", "1/5": "eee", "1/6": "fff"}
	res := Materialize(events, hashes, []string{"1/1", "1/2", "1/3", "1/4", "1/5", "1/6"})
	ready, needs := res.Suggestions()

	if len(ready) != 1 || ready[0].Block != "1/1" || ready[0].Replacement != "fixed wording" {
		t.Fatalf("applicable = %+v, want only 1/1", ready)
	}
	if ready[0].Reason != "" {
		t.Errorf("an applicable suggestion carries no reason: %q", ready[0].Reason)
	}
	want := map[string]string{
		"1/2": ReasonStale,
		"1/3": ReasonNoHash,
		"1/4": ReasonSuperseded,
		"9/9": ReasonOrphaned,
	}
	if len(needs) != len(want) {
		t.Fatalf("needs confirmation = %+v, want %d", needs, len(want))
	}
	for _, s := range needs {
		if s.Reason != want[s.Block] {
			t.Errorf("%s reason = %q, want %q", s.Block, s.Reason, want[s.Block])
		}
		// The quote is what makes an unapplicable suggestion actionable by
		// hand: it says what the reviewer was looking at.
		if s.Quote == "" || s.Replacement == "" {
			t.Errorf("%s = %+v, want quote and replacement", s.Block, s)
		}
	}
	// A comment is not a suggestion, and an empty replacement is not one either.
	for _, s := range append(ready, needs...) {
		if s.Block == "1/5" || s.Block == "1/6" {
			t.Errorf("%s should not be reported as a suggestion", s.Block)
		}
	}
}

func TestSuggestionsEmptyLog(t *testing.T) {
	ready, needs := Materialize(nil, map[string]string{"1/1": "a"}, []string{"1/1"}).Suggestions()
	if len(ready) != 0 || len(needs) != 0 {
		t.Errorf("empty log = %+v / %+v", ready, needs)
	}
}
