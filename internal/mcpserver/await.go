package mcpserver

import (
	"context"
	"os"
	"time"
)

// pollInterval is how often await_review_done looks at a log. Same reasoning
// as the document watcher: size+mtime polling cannot miss an atomic rename,
// needs no dependency, and behaves the same on macOS and Linux.
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
	seen := make(map[string]int64, len(docs))
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
			// Only re-read logs whose file moved.
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

// moved reports whether any log's size or mtime changed since the last look,
// and records the new stamps.
func moved(docs []string, seen map[string]int64) bool {
	changed := false
	for _, d := range docs {
		path := d + ".feedback.jsonl"
		info, err := os.Stat(path)
		if err != nil {
			// A log that does not exist yet is the normal case before the
			// first comment; the next tick will find it.
			if seen[path] != 0 {
				seen[path] = 0
				changed = true
			}
			continue
		}
		stamp := info.Size()<<32 ^ info.ModTime().UnixNano()
		if seen[path] != stamp {
			seen[path] = stamp
			changed = true
		}
	}
	return changed
}
