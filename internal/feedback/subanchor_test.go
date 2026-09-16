package feedback

import "testing"

const para = "The cap is 500. Retries use no backoff. Failures go to the log."

// The acceptance criterion of issue #49: a note on a sentence survives an
// unrelated edit to the same paragraph, and goes stale when its own sentence
// is edited — rather than silently re-anchoring to whatever now sits there.
func TestASubAnchorSurvivesAnUnrelatedEditAndNotItsOwn(t *testing.T) {
	sub := MakeSub(para, "Retries use no backoff.")
	if sub == nil {
		t.Fatal("the quote is in the paragraph")
	}

	// An unrelated sentence changes, and moves ours along.
	edited := "The cap is 2000, raised last week. Retries use no backoff. Failures go to the log."
	at, ok := Locate(sub, edited)
	if !ok {
		t.Fatal("the sentence is still there, so the note still applies")
	}
	if got := runeSlice(NormalizeSpace(edited), at); got != "Retries use no backoff." {
		t.Errorf("located %q", got)
	}

	// Our own sentence changes.
	if _, ok := Locate(sub, "The cap is 500. Retries use exponential backoff. Failures go to the log."); ok {
		t.Error("the sentence was edited — that must read as stale, not re-anchor to its neighbour")
	}
	// And when it is deleted outright.
	if _, ok := Locate(sub, "The cap is 500. Failures go to the log."); ok {
		t.Error("the sentence is gone")
	}
}

// Two identical sentences in one paragraph: the context is what tells them
// apart, which is the whole reason a bare offset is not enough.
func TestIdenticalSentencesAreToldApartByContext(t *testing.T) {
	text := "Alpha runs first. It is fine. Beta runs next. It is fine. Gamma runs last."
	first := MakeSub(text, "It is fine.")
	if first == nil || first.Start == 0 {
		t.Fatalf("bad anchor: %+v", first)
	}
	// Build the second one by quoting with its own surroundings.
	second := &Sub{Quote: "It is fine.", Prefix: "Beta runs next.", Suffix: "Gamma runs last.", Start: 46, Hash: SubHash("It is fine.")}

	a, ok := Locate(first, text)
	if !ok {
		t.Fatal("first should locate")
	}
	b, ok := Locate(second, text)
	if !ok {
		t.Fatal("second should locate")
	}
	if a.Start == b.Start {
		t.Fatalf("both resolved to the same place (%d) — context did not disambiguate", a.Start)
	}
	if runeSlice(text, a) != "It is fine." || runeSlice(text, b) != "It is fine." {
		t.Error("both should still land on the sentence itself")
	}
	// The first occurrence really is the earlier one.
	if a.Start > b.Start {
		t.Errorf("anchors crossed: %d vs %d", a.Start, b.Start)
	}
}

// Whitespace is normalized on both sides, so a paragraph re-wrapped by an
// editor is not an edit as far as a sub-anchor is concerned.
func TestRewrappingIsNotAnEdit(t *testing.T) {
	sub := MakeSub(para, "Retries use no backoff.")
	rewrapped := "The cap is 500.\n   Retries use\n   no backoff.\nFailures go to the log."
	if _, ok := Locate(sub, rewrapped); !ok {
		t.Error("re-wrapping a paragraph must not orphan a note in it")
	}
}

// MakeSub refuses a quote the block does not contain, so the page can never
// write an anchor that could not resolve even at the moment it was made.
func TestMakeSubRefusesWhatItCannotFind(t *testing.T) {
	for _, q := range []string{"", "   ", "a sentence that is not there"} {
		if got := MakeSub(para, q); got != nil {
			t.Errorf("MakeSub(%q) = %+v, want nil", q, got)
		}
	}
	if MakeSub("", "anything") != nil {
		t.Error("an empty block has nothing to anchor into")
	}
}

// Materialize is where the rule actually bites: a sub-anchored note is stale
// when its *sentence* moved, not when anything in the paragraph did.
func TestMaterializeJudgesASubAnchoredNoteByItsSentence(t *testing.T) {
	sub := MakeSub(para, "Retries use no backoff.")
	e := Event{
		Block: "1/2", Type: TypeComment, Text: "no backoff is wrong here",
		Hash: "original", Author: "berkay", Ts: "2026-07-03T10:00:00Z", Sub: sub,
	}
	// The paragraph changed — so the block hash differs — but not our sentence.
	edited := "The cap is 2000. Retries use no backoff. Failures go to the log."
	res := Materialize([]Event{e}, []Block{{ID: "1/2", Hash: "changed", Text: edited}})
	n := res.States[0].Current
	if n.Stale {
		t.Error("the sentence is intact, so the note stands — judging it by the block hash is the bug this fixes")
	}
	if n.At == nil {
		t.Fatal("the page needs to know where it landed")
	}
	if got := runeSlice(NormalizeSpace(edited), *n.At); got != "Retries use no backoff." {
		t.Errorf("At points at %q", got)
	}

	// Now the sentence itself changes.
	res = Materialize([]Event{e}, []Block{{ID: "1/2", Hash: "changed", Text: "The cap is 500. Retries use exponential backoff. Failures go to the log."}})
	n = res.States[0].Current
	if !n.Stale {
		t.Error("its own sentence was edited — that is stale")
	}
	if n.At != nil {
		t.Error("a note that did not locate must not claim a position")
	}
	if res.Stale != 1 {
		t.Errorf("stale count = %d, want 1", res.Stale)
	}
}

// A note without a sub-anchor keeps exactly the old rule, and an orphaned
// block still wins: there is no text to search when the block is gone.
func TestBlockLevelNotesAreUnchanged(t *testing.T) {
	e := Event{Block: "1/2", Type: TypeComment, Text: "x", Hash: "aaa", Ts: "2026-07-03T10:00:00Z"}
	if n := Materialize([]Event{e}, []Block{{ID: "1/2", Hash: "bbb", Text: para}}).States[0].Current; !n.Stale {
		t.Error("a block-level note is still judged by the block hash")
	}
	if n := Materialize([]Event{e}, []Block{{ID: "1/2", Hash: "aaa", Text: para}}).States[0].Current; n.Stale {
		t.Error("unchanged block, unchanged note")
	}
	withSub := e
	withSub.Sub = MakeSub(para, "The cap is 500.")
	res := Materialize([]Event{withSub}, []Block{{ID: "9/9", Hash: "x", Text: "elsewhere"}})
	if len(res.States) != 1 || !res.States[0].Orphaned {
		t.Fatalf("the block is gone, so the note is orphaned: %+v", res.States)
	}
}

// runeSlice reads back what a Located points at, so the tests assert on text
// rather than on offsets nobody can check by eye.
func runeSlice(text string, at Located) string {
	r := []rune(NormalizeSpace(text))
	if at.Start < 0 || at.End > len(r) || at.Start > at.End {
		return ""
	}
	return string(r[at.Start:at.End])
}
