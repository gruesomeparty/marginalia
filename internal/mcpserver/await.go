package mcpserver

import (
	"context"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// pollInterval is how often await_review_done looks at a log's change token.
// Same reasoning as the document watcher: polling cannot miss an atomic
// rename, needs no dependency, and behaves the same on macOS and Linux — and
// on the disk backend the token is that same size+mtime stamp.
const pollInterval = 500 * time.Millisecond

// DefaultTimeout bounds a wait that nobody bounded. Long enough for a human to
// read a page, short enough that an agent hearing nothing back learns that
// rather than hanging.
const DefaultTimeout = 5 * time.Minute

// MaxTimeout is the ceiling, so a tool call cannot pin a turn open forever.
const MaxTimeout = 30 * time.Minute

// Why the wait watches the logs rather than GET /api/revision: revision counts
// document *re-parses* under --watch, which is a different fact entirely — it
// moves when the author edits and stays put when the reviewer comments.
func waitForDone(ctx context.Context, docs []string, cursor map[string]int, timeout time.Duration) (done bool, reason string, out []DocEvents, err error) {
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
func readAll(docs []string, cursor map[string]int) ([]DocEvents, bool, error) {
	out := make([]DocEvents, 0, len(docs))
	all := true
	for _, d := range docs {
		ev, err := eventsSince(d, cursor[d])
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

// moved reports whether any log's change token differs from the last look,
// and records the new ones.
func moved(docs []string, seen map[string]storage.Token) bool {
	changed := false
	for _, d := range docs {
		// A log that does not exist yet has the zero token, which is the
		// normal case before the first comment; the next tick will find it.
		token, err := feedback.NewStore(d).Changed()
		if err != nil {
			continue
		}
		if seen[d] != token {
			seen[d] = token
			changed = true
		}
	}
	return changed
}
