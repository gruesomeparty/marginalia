package feedback

import (
	"testing"
	"time"
)

func ev(block, typ, text, author, ts string) Event {
	return Event{Block: block, Type: typ, Text: text, Author: author, Ts: ts, Hash: "h1"}
}

func materialize(events []Event) Resolution {
	return Materialize(events, map[string]string{"1/1": "h1", "1/2": "h1"}, []string{"1/1", "1/2"})
}

// The case the feature exists for: a human asks, the agent answers, and the
// answer is visible under the question instead of nowhere.
func TestReplyHangsUnderItsNote(t *testing.T) {
	question := ev("1/1", TypeQuestion, "why 500?", "berkay", "2026-09-16T10:00:00Z")
	answer := ev("1/1", "reply", "the upstream budget is 10s", "agent", "2026-09-16T10:05:00Z")
	answer.ReplyTo = NoteID(question)

	res := materialize([]Event{question, answer})
	if len(res.States) != 1 {
		t.Fatalf("expected one block, got %d", len(res.States))
	}
	st := res.States[0]
	if len(st.History) != 1 {
		t.Fatalf("the reply became a note of its own: %+v", st.History)
	}
	if len(st.History[0].Replies) != 1 {
		t.Fatalf("the answer is not under the question: %+v", st.History[0])
	}
	if got := st.History[0].Replies[0].Event.Text; got != "the upstream budget is 10s" {
		t.Errorf("wrong reply: %q", got)
	}
	// A reply never becomes what the block "says" — otherwise an agent
	// answering a question would change the block's verdict.
	if st.Current.Event.Type != TypeQuestion {
		t.Errorf("the reply took over the block: %s", st.Current.Event.Type)
	}
	// And the counts stay honest: comments is how much the reviewer said.
	if res.Comments != 1 || res.Replies != 1 {
		t.Errorf("comments=%d replies=%d, want 1 and 1", res.Comments, res.Replies)
	}
}

// A reply to a reply is still one conversation about one block.
func TestThreadsAreOneLevelDeep(t *testing.T) {
	question := ev("1/1", TypeQuestion, "why 500?", "berkay", "2026-09-16T10:00:00Z")
	answer := ev("1/1", "reply", "budget", "agent", "2026-09-16T10:05:00Z")
	answer.ReplyTo = NoteID(question)
	followUp := ev("1/1", "reply", "then say so in the doc", "berkay", "2026-09-16T10:09:00Z")
	followUp.ReplyTo = NoteID(answer)

	st := materialize([]Event{question, answer, followUp}).States[0]
	if len(st.History) != 1 {
		t.Fatalf("expected one root note, got %d", len(st.History))
	}
	if len(st.History[0].Replies) != 2 {
		t.Fatalf("the follow-up did not re-root: %+v", st.History[0].Replies)
	}
	// Oldest first, so the thread reads in the order it happened.
	if st.History[0].Replies[0].Event.Ts > st.History[0].Replies[1].Event.Ts {
		t.Error("replies are out of order")
	}
}

// A reply whose target is not in this log is surfaced, not swallowed.
func TestDanglingReplyIsSurfaced(t *testing.T) {
	orphanReply := ev("1/1", "reply", "answering something that is not here", "agent", "2026-09-16T10:05:00Z")
	orphanReply.ReplyTo = "deadbeef0000"
	st := materialize([]Event{orphanReply}).States[0]
	if len(st.History) != 1 || !st.History[0].Dangling {
		t.Fatalf("a dangling reply was dropped or not marked: %+v", st.History)
	}
}

// A cycle can only come from a hand-edited file, and must not hang the render.
func TestCyclicRepliesTerminate(t *testing.T) {
	a := ev("1/1", "reply", "a", "x", "2026-09-16T10:00:00Z")
	b := ev("1/1", "reply", "b", "x", "2026-09-16T10:01:00Z")
	a.ReplyTo = NoteID(b)
	b.ReplyTo = NoteID(a)
	done := make(chan Resolution, 1)
	go func() { done <- materialize([]Event{a, b}) }()
	select {
	case res := <-done:
		if len(res.States) == 0 {
			t.Fatal("the cycle produced nothing at all")
		}
	case <-timeout():
		t.Fatal("materializing a cycle did not terminate")
	}
}

// Every log written before replies existed must materialize exactly as it did.
func TestLogsWithoutRepliesAreUnchanged(t *testing.T) {
	one := ev("1/1", TypeComment, "first", "berkay", "2026-09-16T10:00:00Z")
	two := ev("1/2", TypeApprove, "", "berkay", "2026-09-16T10:01:00Z")
	res := materialize([]Event{one, two})
	if res.Comments != 2 || res.Replies != 0 {
		t.Errorf("comments=%d replies=%d, want 2 and 0", res.Comments, res.Replies)
	}
	for _, st := range res.States {
		for _, n := range st.History {
			if len(n.Replies) != 0 || n.Dangling {
				t.Errorf("a plain note grew a thread: %+v", n)
			}
			if n.ID == "" {
				t.Error("a note has no id, so nothing could ever answer it")
			}
		}
	}
}

// The id has to survive the export/import round trip, where the document path
// is rewritten — otherwise every thread breaks when a shared review comes home.
func TestNoteIDIgnoresTheDocumentPath(t *testing.T) {
	here := ev("1/1", TypeQuestion, "why 500?", "berkay", "2026-09-16T10:00:00Z")
	here.Doc = "/home/berkay/spec.md"
	there := here
	there.Doc = "/tmp/downloads/spec.md"
	if NoteID(here) != NoteID(there) {
		t.Error("the id changes with the document path — a thread would not survive import")
	}
	// And it is still specific enough to tell two notes apart.
	other := here
	other.Text = "why 400?"
	if NoteID(here) == NoteID(other) {
		t.Error("two different notes share an id")
	}
}

func timeout() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Second)
		close(ch)
	}()
	return ch
}
