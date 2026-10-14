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
	if row.Title == "" && len(r.Meta.Docs) > 0 {
		row.Title = r.Meta.Docs[0]
	}
	entries := r.Server.Entries()
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
	return row
}

// handleRoot is the inbox.
func (h *Host) handleRoot(w http.ResponseWriter, r *http.Request) {
	tab := r.URL.Query().Get("tab")
	in := web.Inbox{Tab: tab}
	for _, row := range h.rows() {
		in.All++
		if row.Status == web.StatusDone {
			in.Done++
		} else {
			in.NeedsMe++
		}
		if web.InTab(row.Status, tab) {
			in.Rows = append(in.Rows, row)
		}
	}
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

// handleReviews is the inbox as JSON: the same rows, the same statuses.
func (h *Host) handleReviews(w http.ResponseWriter, r *http.Request) {
	tab := r.URL.Query().Get("tab")
	rows := []web.InboxRow{}
	for _, row := range h.rows() {
		if tab == "" || web.InTab(row.Status, tab) {
			rows = append(rows, row)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(rows)
}
