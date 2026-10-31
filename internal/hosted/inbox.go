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

// rows is every review as the inbox lists it, newest activity first. It is
// built from what the host already holds — the parsed revision and the log
// in memory — so rendering it costs the store nothing; on a backend that
// bills per call that is the difference between a front page and a bill.
func (h *Host) rows() []web.InboxRow {
	h.mu.RLock()
	reviews := make([]*Review, 0, len(h.reviews))
	for _, r := range h.reviews {
		reviews = append(reviews, r)
	}
	h.mu.RUnlock()
	out := make([]web.InboxRow, 0, len(reviews))
	for _, r := range reviews {
		out = append(out, h.row(r))
	}
	sort.Slice(out, func(i, j int) bool {
		// A review an agent is stopped on comes first (issue #220).
		if out[i].Urgent != out[j].Urgent {
			return out[i].Urgent
		}
		if !out[i].LastActivity.Equal(out[j].LastActivity) {
			return out[i].LastActivity.After(out[j].LastActivity)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// row files one review: its status, from the log replayed against the
// current revision, and its counts, from the same materialization the page
// and review_status report — never a second tally.
func (h *Host) row(r *Review) web.InboxRow {
	published, _ := time.Parse(time.RFC3339, r.Meta.Created)
	last, _ := time.Parse(time.RFC3339, r.Meta.Updated)
	row := web.InboxRow{
		ID: r.Meta.ID, URL: h.URL(r.Meta.ID), Title: r.Meta.Title, Agent: r.Meta.Agent,
		Docs: len(r.Meta.Docs), Revision: r.Meta.Revision, Published: published, LastActivity: last,
	}
	row.Names = append([]string(nil), r.Meta.Docs...)
	switch {
	case r.Meta.Review != "":
		row.Framing = r.Meta.Review
	case r.Meta.Config != "":
		row.Framing = web.FramingCustom
	}
	if row.Title == "" && len(r.Meta.Docs) > 0 {
		row.Title = r.Meta.Docs[0]
	}
	entries := r.Server.Entries()
	rule := r.Server.Review().Completion()
	done := len(entries) > 0
	var human bool
	var lastDone, lastAgent time.Time
	for i := range entries {
		e := &entries[i]
		events, err := e.Store.Load()
		if err != nil {
			h.logf("marginalia: inbox: %s: %v\n", e.Store.Path(), err)
			continue
		}
		blocks := make([]feedback.Block, 0, len(e.Doc.Blocks))
		for _, b := range e.Doc.Blocks {
			blocks = append(blocks, feedback.Block{ID: b.ID, Hash: b.Hash, Text: b.PlainText})
		}
		res := feedback.Materialize(events, blocks)
		res.Complete(rule)
		row.Comments += res.Comments
		row.Outstanding += res.Outstanding
		row.Stale += res.Stale
		for _, st := range res.States {
			if st.Current.Unclaimed {
				row.Unclaimed++
			}
		}
		if !res.Done {
			done = false
		}
		for _, ev := range events {
			ts, err := time.Parse(time.RFC3339, ev.Ts)
			if err != nil {
				continue
			}
			if ts.After(row.LastActivity) {
				row.LastActivity = ts
			}
			switch ev.Type {
			case review.TypeReply, review.TypeAddressed:
				// The agent's side of the loop.
				if ts.After(lastAgent) {
					lastAgent = ts
				}
			case review.TypeReviewDone:
				human = true
				if ts.After(lastDone) {
					lastDone = ts
				}
			default:
				human = true
			}
		}
	}
	switch {
	case done && lastAgent.After(lastDone):
		row.Status = web.StatusReplied
	case done:
		row.Status = web.StatusDone
	case human:
		row.Status = web.StatusInProgress
	default:
		row.Status = web.StatusAwaiting
	}
	row.StatusLabel = web.StatusLabel(row.Status)
	h.urgency(r, &row)
	return row
}

// urgency copies what the agent said about its wait onto the row: blocking,
// the due-by and whether it has passed, and since when it has waited (the
// revision it is waiting on, so a republish restarts the clock). A finished
// review is never urgent, whatever it was published with.
func (h *Host) urgency(r *Review, row *web.InboxRow) {
	row.Blocking = r.Meta.Blocking
	row.WaitingSince, _ = time.Parse(time.RFC3339, r.Meta.Updated)
	if t, err := time.Parse(time.RFC3339, r.Meta.DueBy); err == nil {
		t = t.UTC()
		row.DueBy = &t
		row.Overdue = row.Status != web.StatusDone && t.Before(h.opts.Now())
	}
	row.Urgent = row.Status != web.StatusDone && (row.Blocking || row.Overdue)
}

// handleRoot is the inbox. The tab counts are taken after the other
// filters, so a search says how many of its matches each tab holds.
func (h *Host) handleRoot(w http.ResponseWriter, r *http.Request) {
	f := web.ParseInboxFilter(r.URL.Query())
	in := web.Inbox{Tab: f.Tab, Filter: f, Now: h.opts.Now()}
	framings, agents := map[string]bool{}, map[string]bool{}
	for _, row := range h.rows() {
		if k := web.FramingKey(row.Framing); !framings[k] {
			framings[k] = true
			in.Framings = append(in.Framings, k)
		}
		if row.Agent != "" && !agents[row.Agent] {
			agents[row.Agent] = true
			in.Agents = append(in.Agents, row.Agent)
		}
		if !f.Match(row) {
			continue
		}
		in.All++
		if row.Status == web.StatusDone {
			in.Done++
		} else {
			in.NeedsMe++
		}
		if web.InTab(row.Status, f.Tab) {
			in.Rows = append(in.Rows, row)
		}
	}
	sort.Strings(in.Framings)
	sort.Strings(in.Agents)
	// Render into a buffer first, as the review page does: half an inbox
	// under a 200 is worse than an honest 500.
	var buf bytes.Buffer
	if err := web.RenderInbox(&buf, in); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// handleReviews is the inbox as JSON: the same rows, the same statuses, the
// same filters.
func (h *Host) handleReviews(w http.ResponseWriter, r *http.Request) {
	f := web.ParseInboxFilter(r.URL.Query())
	rows := []web.InboxRow{}
	for _, row := range h.rows() {
		if (f.Tab == "" || web.InTab(row.Status, f.Tab)) && f.Match(row) {
			rows = append(rows, row)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(rows)
}
