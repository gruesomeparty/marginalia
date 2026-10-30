package hosted

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// AgentWaitPath is the long-poll an agent holding the /mcp credential may
// call over plain HTTP (issue #186), for one review, and nothing else of it.
// It sits under /mcp/ on purpose. That prefix is already the agents' at the
// edge: Access bypasses /mcp and its subpaths, and the Worker refuses one
// with no bearer, or a wrong static token, before it would wake the
// container. The page routes stay human-only, structurally.
const AgentWaitPath = "/mcp/reviews/{id}/wait"

// agentWaitMax caps one long-poll, as /api/wait does, below the idle
// timeouts proxies tend to apply.
const agentWaitMax = 60 * time.Second

// agentWaitTick is how often a parked wait looks at the logs' change
// tokens. They are in memory (storage.WriteThrough), so a look costs no
// store read.
const agentWaitTick = 250 * time.Millisecond

// handleAgentWait answers `?since=N[&timeout=SECONDS]` as soon as the
// review's logs hold more than N events, or once every document carries a
// review_done, or at the timeout (default 30s, at most 60s). Without since
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
	for {
		n, done, err := h.tally(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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

// tally counts a review's events across its documents, and whether every
// document's log carries a review_done, from the same logs the hosted
// tools read: in memory, so a look costs no store read.
func (h *Host) tally(id string) (n uint64, done bool, err error) {
	_, targets, err := h.targets(id, "")
	if err != nil {
		return 0, false, err
	}
	done = len(targets) > 0
	for _, t := range targets {
		events, err := t.Store.Load()
		if err != nil {
			return 0, false, err
		}
		finished := false
		for _, e := range events {
			finished = finished || e.Type == feedback.TypeReviewDone
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
