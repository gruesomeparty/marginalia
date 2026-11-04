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
		row.Changed += r.Server.ChangedCount(e)
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
// revision it is waiting on, so a republish restarts the clock). Once that
// revision is finished none of it shows, whatever it was published with.
func (h *Host) urgency(r *Review, row *web.InboxRow) {
	// The wait is on the revision the agent published: it is over once that
	// revision has its Dones under the review's rule, as the agent's own
	// wait reads it. So a blocking republish after Done is urgent again,
	// and an agent's reply after Done (no new revision) is not.
	_, finished, err := h.tally(r.Meta.ID)
	open := err == nil && !finished
	row.Blocking = r.Meta.Blocking && open
	row.WaitingSince, _ = time.Parse(time.RFC3339, r.Meta.Updated)
	if t, err := time.Parse(time.RFC3339, r.Meta.DueBy); err == nil && open {
		t = t.UTC()
		row.DueBy = &t
		row.Overdue = t.Before(h.opts.Now())
	}
	row.Urgent = open && (row.Blocking || row.Overdue)
	// The status is read off the whole log, where the last round's Done
	// still stands. A review the agent is waiting on again is not done:
	// file it where the reviewer looks.
	if row.Urgent && (row.Status == web.StatusDone || row.Status == web.StatusReplied) {
		row.Status = web.StatusInProgress
		row.StatusLabel = web.StatusLabel(row.Status)
	}
}

// archivedRows is the archive as the inbox lists it: what the metadata
// says, stamped with when it was archived. No counts — nothing renders an
// archived review, so its logs are not read.
func (h *Host) archivedRows() []web.InboxRow {
	var out []web.InboxRow
	for _, a := range h.ArchivedReviews() {
		published, _ := time.Parse(time.RFC3339, a.Meta.Created)
		row := web.InboxRow{
			ID: a.Meta.ID, Title: a.Meta.Title, Agent: a.Meta.Agent,
			Docs: len(a.Meta.Docs), Revision: a.Meta.Revision, Published: published, LastActivity: a.At,
			Names: append([]string(nil), a.Meta.Docs...), Status: web.StatusArchived,
		}
		switch {
		case a.Meta.Review != "":
			row.Framing = a.Meta.Review
		case a.Meta.Config != "":
			row.Framing = web.FramingCustom
		}
		if row.Title == "" && len(a.Meta.Docs) > 0 {
			row.Title = a.Meta.Docs[0]
		}
		row.StatusLabel = web.StatusLabel(row.Status)
		out = append(out, row)
	}
	return out
}

// handleRoot is the inbox. The tab counts are taken after the other
// filters, so a search says how many of its matches each tab holds.
func (h *Host) handleRoot(w http.ResponseWriter, r *http.Request) {
	f := web.ParseInboxFilter(r.URL.Query())
	in := web.Inbox{Tab: f.Tab, Filter: f, Now: h.opts.Now()}
	framings, agents := map[string]bool{}, map[string]bool{}
	for _, row := range append(h.rows(), h.archivedRows()...) {
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
		switch row.Status {
		case web.StatusArchived:
			in.Archived++
		case web.StatusDone:
			in.All++
			in.Done++
		default:
			in.All++
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
// same filters. With no tab it is every open review; the archive is
// tab=archived.
func (h *Host) handleReviews(w http.ResponseWriter, r *http.Request) {
	f := web.ParseInboxFilter(r.URL.Query())
	tab := f.Tab
	if tab == "" {
		tab = web.TabAll
	}
	all := h.rows()
	if tab == web.TabArchived {
		all = h.archivedRows()
	}
	rows := []web.InboxRow{}
	for _, row := range all {
		if web.InTab(row.Status, tab) && f.Match(row) {
			rows = append(rows, row)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(rows)
}
