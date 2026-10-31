package mcpserver

import (
	"context"
	"strings"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// pollInterval is how often await_review_done looks at a log's change token.
// Same reasoning as the document watcher: polling cannot miss an atomic
// rename, needs no dependency, and behaves the same on macOS and Linux — and
// on the disk backend the token is that same size+mtime stamp.
const pollInterval = 500 * time.Millisecond

// DefaultTimeout bounds a wait that nobody bounded. It sits under the 60s that
// MCP clients commonly allow a tool call (claude.ai's connector among them,
// issue #182): a wait the client abandons returns nothing at all, while one
// that times out returns the cursor, so the agent loops on short waits rather
// than holding one long one.
const DefaultTimeout = 45 * time.Second

// MaxTimeout is the ceiling, so a tool call cannot pin a turn open forever.
const MaxTimeout = 30 * time.Minute

// Why the wait watches the logs rather than GET /api/revision: revision counts
// document *re-parses* under --watch, which is a different fact entirely — it
// moves when the author edits and stays put when the reviewer comments.
//
// A tap on a multiple-choice requester note returns it with reason "answer"
// and the pick in Choices. An unanswered question in the same batch wins
// the reason, so it is never skipped; the choices are reported either way.
//
// The wait also returns, with reason "question", as soon as a question sits
// past the cursor and nobody has replied to it yet: a reviewer who asks the
// agent something mid-review should get an answer while they are still on
// the page, not after Done (issue #182). Only a `question` wakes it — a
// comment is the reviewer talking, a question is the reviewer waiting. Done
// outranks it, so a finished review always reads as finished.
//
// WaitForDone is exported for the hosted tools, which wait on the same terms
// over targets of their own.
func WaitForDone(ctx context.Context, docs []Target, cursor map[string]int, timeout time.Duration) (done bool, reason string, out []DocEvents, err error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	deadline := time.After(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	seen := make(map[string]storage.Token, len(docs))
	for {
		// Read first, then wait: a review already finished must return at
		// once rather than after a tick.
		out, done, err = readAll(docs, cursor)
		if err != nil {
			return false, "", nil, err
		}
		if done {
			return true, "review_done", out, nil
		}
		// A question outranks a choice: the agent owes it an answer, and
		// the choices are in the output whichever reason wins.
		if asked(out) {
			return false, "question", out, nil
		}
		if len(Choices(out)) > 0 {
			return false, "answer", out, nil
		}
		select {
		case <-ctx.Done():
			// The client cancelled or the process is going down. Hand back
			// what arrived anyway — the events are the point, not the wait.
			return false, "cancelled", out, nil
		case <-deadline:
			// A timeout is not an error: the human is simply not finished,
			// and the caller gets everything new so it can say so.
			return false, "timeout", out, nil
		case <-ticker.C:
			// Only re-read logs whose token moved.
			if !moved(docs, seen) {
				continue
			}
		}
	}
}

// readAll reads every document's log from its cursor, and reports whether all
// of them are finished.
func readAll(docs []Target, cursor map[string]int) ([]DocEvents, bool, error) {
	out := make([]DocEvents, 0, len(docs))
	all := true
	for _, d := range docs {
		ev, err := EventsSince(d, cursor[d.Name])
		if err != nil {
			return nil, false, err
		}
		if !ev.Done {
			all = false
		}
		out = append(out, ev)
	}
	return out, all, nil
}

// asked reports whether any document has an unanswered question past its
// cursor. A reply always follows its question, so if the question is past
// the cursor its answer is too, and one already answered does not wake the
// wait again.
func asked(docs []DocEvents) bool {
	for _, d := range docs {
		answered := map[string]bool{}
		for _, e := range d.Events {
			if e.Type == feedback.TypeReply {
				answered[e.ReplyTo] = true
			}
		}
		for _, e := range d.Events {
			if e.Type == feedback.TypeQuestion && !answered[e.ID] {
				return true
			}
		}
	}
	return false
}

// Choice is the reviewer's tap on a multiple-choice requester note
// (issue #215): which note, which option, who.
type Choice struct {
	Doc    string `json:"doc"`
	Note   string `json:"note" jsonschema:"the requester note's id (req:...)"`
	Choice string `json:"choice"`
	Author string `json:"author,omitempty"`
}

// Choices lists the options reviewers picked in these events, oldest first.
// A choice wakes the wait with reason "answer", so the agent can proceed on
// the decision without waiting for Done.
func Choices(docs []DocEvents) []Choice {
	out := []Choice{} // never null in a tool's output
	for _, d := range docs {
		for _, e := range d.Events {
			c, ok := e.Fields[review.ChoiceField]
			if e.Type != feedback.TypeReply || !ok || !strings.HasPrefix(e.ReplyTo, review.RequesterPrefix) {
				continue
			}
			out = append(out, Choice{Doc: d.Doc, Note: e.ReplyTo, Choice: c, Author: e.Author})
		}
	}
	return out
}

// moved reports whether any log's change token differs from the last look,
// and records the new ones.
func moved(docs []Target, seen map[string]storage.Token) bool {
	changed := false
	for _, d := range docs {
		// A log that does not exist yet has the zero token, which is the
		// normal case before the first comment; the next tick will find it.
		token, err := d.Store.Changed()
		if err != nil {
			continue
		}
		if seen[d.Name] != token {
			seen[d.Name] = token
			changed = true
		}
	}
	return changed
}
