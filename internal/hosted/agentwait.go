package hosted

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// agentWaitMax caps one long-poll, as /api/wait does, below the idle
// timeouts proxies tend to apply.
const agentWaitMax = 60 * time.Second

// agentWaitTick is how often a parked wait looks at the logs' change
// tokens. They are in memory (storage.WriteThrough), so a look costs no
// store read.
const agentWaitTick = 250 * time.Millisecond

// handleAgentWait answers `?since=N[&timeout=SECONDS]` as soon as the
// review's logs hold more than N events, or once every document carries a
// review_done for the current revision, or at the timeout (default 30s, at most 60s). Without since
// it answers at once, with the count to start from.
//
// It answers in the shape of the page's /api/wait, but counts the logs, not
// what one in-memory server saw: a republish builds a new server per
// revision and a restart rebuilds them all, and a cursor that reset on
// either would leave the agent waiting on notes it will never be told of.
// The log is append-only, so its length is a cursor that never moves back.
func (h *Host) handleAgentWait(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidID(id) || h.Review(id) == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	timeout := 30 * time.Second
	if v := q.Get("timeout"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil || secs < 0 {
			http.Error(w, "timeout must be a whole number of seconds", http.StatusBadRequest)
			return
		}
		timeout = min(time.Duration(secs)*time.Second, agentWaitMax)
	}
	var since uint64
	parked := q.Get("since") != ""
	if parked {
		var err error
		if since, err = strconv.ParseUint(q.Get("since"), 10, 64); err != nil {
			http.Error(w, "since must be the events count a previous wait returned", http.StatusBadRequest)
			return
		}
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(agentWaitTick)
	defer tick.Stop()
	var (
		seen []storage.Token
		n    uint64
		done bool
	)
	for {
		// Count again only when a log moved or a revision was published: a
		// parked wait must not decode every log four times a second.
		tokens, err := h.tokens(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if seen == nil || !slices.Equal(tokens, seen) {
			if n, done, err = h.tally(id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			seen = tokens
		}
		if !parked || done || n > since {
			agentAnswer(w, waitAnswer{Events: n, Done: done})
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			agentAnswer(w, waitAnswer{Events: n, Done: done, TimedOut: true})
			return
		case <-r.Context().Done():
			return
		}
	}
}

type waitAnswer struct {
	Events   uint64 `json:"events"`
	Done     bool   `json:"done"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// tokens is what a wait watches: the review's revision, then each log's
// change token. All in memory, so a look costs no store read.
func (h *Host) tokens(id string) ([]storage.Token, error) {
	r, targets, err := h.targets(id, "")
	if err != nil {
		return nil, err
	}
	out := []storage.Token{storage.Token(strconv.Itoa(r.Meta.Revision))}
	for _, t := range targets {
		token, err := t.Store.Changed()
		if err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, nil
}

// tally counts a review's events across its documents, and whether every
// document's log carries a review_done for the current revision: one written
// after it was published (Meta.From), since the logs outlive a republish and
// the last round's review_done must not end this round's wait.
func (h *Host) tally(id string) (n uint64, done bool, err error) {
	r, targets, err := h.targets(id, "")
	if err != nil {
		return 0, false, err
	}
	done = len(targets) > 0
	for _, k := range r.Meta.Retired {
		n += uint64(k)
	}
	for _, t := range targets {
		events, err := t.Store.Load()
		if err != nil {
			return 0, false, err
		}
		finished := false
		for i, e := range events {
			finished = finished || (e.Type == feedback.TypeReviewDone && i >= r.Meta.From[t.Name])
		}
		n += uint64(len(events))
		done = done && finished
	}
	return n, done, nil
}

func agentAnswer(w http.ResponseWriter, a waitAnswer) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(a)
}
