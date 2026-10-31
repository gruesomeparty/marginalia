package hosted

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// The stats page (issue #216): is the loop working? How long reviews wait
// for a first look and for Done, how many rounds they take, what reviewers
// say, and whether the agent's fixes hold up — per agent, over a period.
//
// Like the inbox it is built from what the host already holds: the current
// revision, the logs in memory through the write-through cache, and
// feedback.Materialize. A page view costs the store nothing.

// reviewStats is one review's contribution, before it is summed per agent.
type reviewStats struct {
	agent, framing string
	created        time.Time
	firstLook      time.Duration
	looked         bool
	toDone         time.Duration
	done           bool
	rounds         int
	notes          map[string]int
	claims         web.Claims
}

// statsOf reads one review. Only the documents the current revision serves
// are counted: a retired document's log is not held in memory, and reading
// it would cost the store a call per page view.
func (h *Host) statsOf(r *Review) reviewStats {
	created, _ := time.Parse(time.RFC3339, r.Meta.Created)
	s := reviewStats{agent: r.Meta.Agent, framing: framingOf(r.Meta), created: created, rounds: r.Meta.Rounds, notes: map[string]int{}}
	rule := r.Server.Review().Completion()
	var firstHuman, doneAt time.Time
	entries := r.Server.Entries()
	allDone := len(entries) > 0
	for _, e := range entries {
		events, err := e.Store.Load()
		if err != nil {
			h.logf("marginalia: stats: %s: %v\n", e.Store.Path(), err)
			allDone = false
			continue
		}
		for _, ev := range events {
			if agentSide(ev.Type) {
				continue
			}
			if !review.IsProtocol(ev.Type) {
				s.notes[ev.Type]++
			}
			ts, err := time.Parse(time.RFC3339, ev.Ts)
			if err != nil {
				continue
			}
			if firstHuman.IsZero() || ts.Before(firstHuman) {
				firstHuman = ts
			}
		}
		// Done is the review's own rule, as the inbox reads it: a document
		// is done when its log first meets it, the review when every
		// document has.
		if at, ok := firstMet(rule, events); ok {
			if at.After(doneAt) {
				doneAt = at
			}
		} else {
			allDone = false
		}
		blocks := make([]feedback.Block, 0, len(e.Doc.Blocks))
		for _, b := range e.Doc.Blocks {
			blocks = append(blocks, feedback.Block{ID: b.ID, Hash: b.Hash, Text: b.PlainText})
		}
		for _, st := range feedback.Materialize(events, blocks).States {
			for _, n := range st.History {
				countClaim(&s.claims, n)
			}
		}
	}
	if finished, _ := revisionFinished(r); finished {
		s.rounds++
	}
	firstDone := time.Time{}
	if allDone {
		firstDone = doneAt
	}
	if !firstHuman.IsZero() && !created.IsZero() {
		s.firstLook, s.looked = firstHuman.Sub(created), true
	}
	if !firstDone.IsZero() && !created.IsZero() {
		s.toDone, s.done = firstDone.Sub(created), true
	}
	return s
}

// agentSide is what an agent writes: an answer or a claim. Neither is the
// reviewer looking.
func agentSide(typ string) bool {
	return typ == review.TypeReply || typ == review.TypeAddressed
}

// countClaim files a note that received an `addressed`: where the reviewer
// left it, and whether the claim was made against unchanged text.
func countClaim(c *web.Claims, n feedback.Note) {
	claimed := false
	for _, p := range n.Progress {
		if p.Event.Type == review.TypeAddressed {
			claimed = true
			break
		}
	}
	if !claimed {
		return
	}
	c.Addressed++
	switch n.Status {
	case feedback.StatusConfirmed:
		c.Confirmed++
	case feedback.StatusReopened:
		c.Reopened++
	default:
		c.Pending++
	}
	if n.Unclaimed {
		c.Unclaimed++
	}
}

// framingOf is a review's framing filter key, as the inbox computes it.
func framingOf(m Meta) string {
	switch {
	case m.Review != "":
		return web.FramingKey(m.Review)
	case m.Config != "":
		return web.FramingCustom
	}
	return web.FramingDefault
}

// stats sums every review that passes the filters, per agent and in total.
func (h *Host) stats(period, agent, framing string) web.Stats {
	period, window := web.ParsePeriod(period)
	out := web.Stats{Period: period, Agent: agent, Framing: framing}
	var since time.Time
	if window > 0 {
		since = h.opts.Now().UTC().Add(-window)
		out.Since = &since
	}
	h.mu.RLock()
	reviews := make([]*Review, 0, len(h.reviews))
	for _, r := range h.reviews {
		reviews = append(reviews, r)
	}
	h.mu.RUnlock()
	type acc struct {
		web.AgentStats
		looks, dones []time.Duration
	}
	newAcc := func(name string) *acc { return &acc{AgentStats: web.AgentStats{Agent: name, Notes: map[string]int{}}} }
	total := newAcc("")
	per := map[string]*acc{}
	agents, framings := map[string]bool{}, map[string]bool{}
	for _, r := range reviews {
		s := h.statsOf(r)
		if s.agent != "" {
			agents[s.agent] = true
		}
		framings[s.framing] = true
		if (window > 0 && s.created.Before(since)) || (agent != "" && s.agent != agent) || (framing != "" && s.framing != framing) {
			continue
		}
		a := per[s.agent]
		if a == nil {
			a = newAcc(s.agent)
			per[s.agent] = a
		}
		for _, t := range []*acc{a, total} {
			t.Reviews++
			t.Rounds += s.rounds
			if s.looked {
				t.looks = append(t.looks, s.firstLook)
			}
			if s.done {
				t.Done++
				t.dones = append(t.dones, s.toDone)
			}
			for typ, n := range s.notes {
				t.Notes[typ] += n
			}
			t.Claims.Addressed += s.claims.Addressed
			t.Claims.Confirmed += s.claims.Confirmed
			t.Claims.Reopened += s.claims.Reopened
			t.Claims.Pending += s.claims.Pending
			t.Claims.Unclaimed += s.claims.Unclaimed
		}
	}
	finish := func(a *acc) web.AgentStats {
		a.FirstLook, a.TimeToDone = web.Summarize(a.looks), web.Summarize(a.dones)
		return a.AgentStats
	}
	out.Total = finish(total)
	out.Agents = []web.AgentStats{}
	for _, a := range per {
		out.Agents = append(out.Agents, finish(a))
	}
	sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].Agent < out.Agents[j].Agent })
	for a := range agents {
		out.AllAgents = append(out.AllAgents, a)
	}
	for f := range framings {
		out.AllFramings = append(out.AllFramings, f)
	}
	sort.Strings(out.AllAgents)
	sort.Strings(out.AllFramings)
	return out
}

func (h *Host) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s := h.stats(q.Get("period"), q.Get("agent"), q.Get("framing"))
	var buf bytes.Buffer
	if err := web.RenderStats(&buf, s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (h *Host) handleAPIStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(h.stats(q.Get("period"), q.Get("agent"), q.Get("framing")))
}

// revisionFinished reports whether a revision was finished under the
// review's completion rule: every document's log, from the point it had
// reached when the revision was published (Meta.From), meets it.
func revisionFinished(r *Review) (bool, error) {
	rule := r.Server.Review().Completion()
	entries := r.Server.Entries()
	if len(entries) == 0 {
		return false, nil
	}
	for _, e := range entries {
		events, err := e.Store.Load()
		if err != nil {
			return false, err
		}
		if !rule.Met(feedback.Reviewers(events[min(r.Meta.From[e.Rel], len(events)):])) {
			return false, nil
		}
	}
	return true, nil
}

// firstMet is when a log first met the completion rule: the time of the
// review_done that tipped it.
func firstMet(rule review.Completion, events []feedback.Event) (time.Time, bool) {
	for i, ev := range events {
		if ev.Type != review.TypeReviewDone || !rule.Met(feedback.Reviewers(events[:i+1])) {
			continue
		}
		ts, err := time.Parse(time.RFC3339, ev.Ts)
		return ts, err == nil
	}
	return time.Time{}, false
}
