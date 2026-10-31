package server

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// Waiting for the human (issue #44). An agent that handed a document over had
// two options: poll the JSONL on a timer it invented, or hold a terminal open
// and hope. These are the third: a long-poll that answers the moment anything
// is saved, and a channel that closes when every served document has been
// marked done, which is what `serve --until-done` exits on.
//
// Both count only what this process wrote. A review_done already in a log
// from an earlier round is the previous handover's answer, not this one's.
// A document is done when this process's Dones meet the review's completion
// rule (issue #53): one of them by default, or everyone, or a quorum.

// maxWait caps one long-poll, below the idle timeouts proxies tend to apply.
const maxWait = 60 * time.Second

type changes struct {
	mu   sync.Mutex
	n    uint64        // events appended since the server started
	tick chan struct{} // closed and replaced on every append
	done map[string]bool
	// finished is who has said Done on each document through this server:
	// a document is done once those meet the review's completion rule.
	finished map[string][]string
	allDone  chan struct{} // closed once every document has a review_done
	// stopping is closed when the HTTP server starts shutting down. Shutdown
	// does not cancel request contexts, so a parked long-poll would hold the
	// drain to its deadline and turn a clean exit into an error.
	stopping chan struct{}
	stopOnce sync.Once
}

func newChanges() changes {
	return changes{tick: make(chan struct{}), done: map[string]bool{}, finished: map[string][]string{}, allDone: make(chan struct{}), stopping: make(chan struct{})}
}

// stopWaits answers every parked long-poll, now and from here on.
func (s *Server) stopWaits() { s.changes.stopOnce.Do(func() { close(s.changes.stopping) }) }

// recorded wakes every waiter after an event reaches the log.
func (s *Server) recorded(target *Entry, e feedback.Event) {
	c := &s.changes
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	close(c.tick)
	c.tick = make(chan struct{})
	if e.Type == feedback.TypeReviewDone {
		s.finished(target)
	}
	if e.Type != feedback.TypeReviewDone || c.done[target.Rel] {
		return
	}
	c.finished[target.Rel] = append(c.finished[target.Rel], e.Author)
	// Who counts as a reviewer is read off the whole log; whose Done counts
	// is only this process's.
	var participants []string
	if events, err := target.Store.Load(); err == nil {
		_, participants = feedback.Reviewers(events)
	}
	if !s.Review().Completion().Met(c.finished[target.Rel], participants) {
		return
	}
	c.done[target.Rel] = true
	if len(c.done) == len(s.docs) {
		close(c.allDone)
	}
}

// Done is closed once every served document has been marked done by this
// server — per document from its page, or all at once by session_done.
func (s *Server) Done() <-chan struct{} { return s.changes.allDone }

// Progress reports how many served documents this server has seen marked
// done, and how many it serves.
func (s *Server) Progress() (done, total int) {
	s.changes.mu.Lock()
	defer s.changes.mu.Unlock()
	return len(s.changes.done), len(s.docs)
}

func (s *Server) snapshot() (n uint64, tick chan struct{}, done bool) {
	c := &s.changes
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n, c.tick, len(c.done) == len(s.docs)
}

// handleWait is the long-poll: `GET /api/wait?since=N[&timeout=SECONDS]`
// answers as soon as more than N events have been saved, or when the timeout
// (default 30s, at most 60s) passes, with the count to pass as `since` next
// time. Without `since` it answers at once, which is how a caller learns
// where to start.
func (s *Server) handleWait(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	timeout := 30 * time.Second
	if v := q.Get("timeout"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs < 0 {
			http.Error(w, "timeout must be a whole number of seconds", http.StatusBadRequest)
			return
		}
		timeout = min(time.Duration(secs)*time.Second, maxWait)
	}
	n, tick, done := s.snapshot()
	if v := q.Get("since"); v != "" && !done {
		since, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			http.Error(w, "since must be the events count a previous wait returned", http.StatusBadRequest)
			return
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for n <= since {
			select {
			case <-tick:
			case <-timer.C:
				writeJSON(w, http.StatusOK, waitResult{Events: n, Done: done, TimedOut: true})
				return
			case <-s.changes.stopping:
				// Shutting down: answer with what is known, and let the
				// caller's next wait find the server gone.
				writeJSON(w, http.StatusOK, waitResult{Events: n, Done: done})
				return
			case <-r.Context().Done():
				return
			}
			n, tick, done = s.snapshot()
		}
	}
	writeJSON(w, http.StatusOK, waitResult{Events: n, Done: done})
}

type waitResult struct {
	Events   uint64 `json:"events"`
	Done     bool   `json:"done"`
	TimedOut bool   `json:"timed_out,omitempty"`
}
