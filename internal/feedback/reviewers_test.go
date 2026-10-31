package feedback

import (
	"testing"

	"github.com/gruesomeparty/marginalia/internal/review"
)

// Issue #53: two reviewers on one block. The block's state is each reviewer's
// latest word and an overall verdict from a fixed rule, not whichever note
// arrived last.
func TestReviewersEachKeepTheirVerdict(t *testing.T) {
	blocks := []Block{{ID: "1/1", Hash: "h"}}
	events := []Event{
		{Block: "1/1", Hash: "h", Type: TypeApprove, Author: "ana", Ts: "2026-10-31T10:00:00Z"},
		{Block: "1/1", Hash: "h", Type: TypeReject, Author: "bo", Text: "no", Ts: "2026-10-31T10:01:00Z"},
		{Block: "1/1", Hash: "h", Type: TypeComment, Author: "ana", Text: "hm", Ts: "2026-10-31T10:02:00Z"},
		{Block: "1/1", Hash: "h", Type: TypeApprove, Author: "ana", Ts: "2026-10-31T10:03:00Z"},
		{Block: "1/1", Hash: "h", Type: TypeApprove, Author: "cy", Ts: "2026-10-31T10:04:00Z"},
	}
	st := Materialize(events, blocks).States[0]
	if st.Overall != TypeReject || st.Current.Event.Author != "bo" {
		t.Errorf("overall %q by %q, want reject by bo: one rejection outranks two later approvals", st.Overall, st.Current.Event.Author)
	}
	want := []Verdict{{Author: "ana", Type: TypeApprove}, {Author: "bo", Type: TypeReject}, {Author: "cy", Type: TypeApprove}}
	if len(st.ByAuthor) != len(want) {
		t.Fatalf("by author: %+v", st.ByAuthor)
	}
	for i, w := range want {
		if st.ByAuthor[i].Author != w.Author || st.ByAuthor[i].Type != w.Type {
			t.Errorf("verdict %d: %+v, want %s %s", i, st.ByAuthor[i], w.Author, w.Type)
		}
	}
	// Bo changes their mind: the rejection no longer stands.
	events = append(events, Event{Block: "1/1", Hash: "h", Type: TypeQuestion, Author: "bo", Text: "why?", Ts: "2026-10-31T10:05:00Z"})
	if got := Materialize(events, blocks).States[0].Overall; got != TypeQuestion {
		t.Errorf("after bo's question: %q", got)
	}
}

// One reviewer reads exactly as before: their latest note stands.
func TestOneReviewerLatestStands(t *testing.T) {
	events := []Event{
		{Block: "1/1", Type: TypeReject, Author: "ana", Ts: "2026-10-31T10:00:00Z"},
		{Block: "1/1", Type: TypeApprove, Author: "ana", Ts: "2026-10-31T10:01:00Z"},
	}
	if got := Materialize(events, []Block{{ID: "1/1"}}).States[0].Overall; got != TypeApprove {
		t.Errorf("one reviewer's latest is %q", got)
	}
}

func TestCompletionRules(t *testing.T) {
	events := []Event{
		{Block: "1/1", Type: TypeComment, Author: "ana", Text: "x", Ts: "1"},
		{Block: "1/1", Type: TypeComment, Author: "bo", Text: "y", Ts: "2"},
		{Block: "1/1", Type: TypeReply, Author: "agent", Text: "z", ReplyTo: "nothing", Ts: "3"},
		{Type: TypeReviewDone, Author: "ana", Ts: "4"},
		{Type: TypeReviewDone, Author: "ana", Ts: "5"},
	}
	res := Materialize(events, []Block{{ID: "1/1"}})
	if !res.Done || res.DoneBy != 1 || res.DoneNeeded != 1 {
		t.Errorf("default rule: done %v, %d of %d", res.Done, res.DoneBy, res.DoneNeeded)
	}
	for _, c := range []struct {
		rule      review.Completion
		done      bool
		have, nee int
	}{
		{review.Completion{Rule: review.CompleteAll}, false, 1, 2}, // the agent's reply makes nobody a reviewer
		{review.Completion{Rule: review.CompleteQuorum, Quorum: 1}, true, 1, 1},
		{review.Completion{Rule: review.CompleteAll, Reviewers: []string{"ana", "bo", "cy"}}, false, 1, 3},
		{review.Completion{Rule: review.CompleteAny, Reviewers: []string{"bo"}}, false, 0, 1}, // ana is not on the list
	} {
		r := res
		r.Complete(c.rule)
		if r.Done != c.done || r.DoneBy != c.have || r.DoneNeeded != c.nee {
			t.Errorf("%+v: done %v, %d of %d", c.rule, r.Done, r.DoneBy, r.DoneNeeded)
		}
	}
	events = append(events, Event{Type: TypeReviewDone, Author: "bo", Ts: "6"})
	res = Materialize(events, []Block{{ID: "1/1"}})
	res.Complete(review.Completion{Rule: review.CompleteAll})
	if !res.Done || res.DoneBy != 2 {
		t.Errorf("everyone done: %v, %d of %d", res.Done, res.DoneBy, res.DoneNeeded)
	}
}
