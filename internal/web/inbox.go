package web

import (
	"io"
	"time"
)

// The inbox is the hosted instance's front page (PRD §5.9): every review,
// newest activity first, with what each one is waiting on. It is
// server-rendered from the host's in-memory state, needs no script to read,
// and is built from the same tokens as the review page so the two cannot
// drift apart.

// Review statuses, as the inbox files them. The words are the reviewer's
// side of the loop: what each one asks of them.
const (
	// StatusAwaiting: published, and the reviewer has said nothing yet.
	StatusAwaiting = "awaiting"
	// StatusInProgress: the reviewer has notes down but has not pressed Done.
	StatusInProgress = "in_progress"
	// StatusDone: every document carries a review_done, and nothing has
	// happened since.
	StatusDone = "done"
	// StatusReplied: the agent answered or claimed after the reviewer
	// finished — the revision loop coming back for a second look.
	StatusReplied = "replied"
)

// StatusLabel is the status as the page shows it.
func StatusLabel(status string) string {
	switch status {
	case StatusAwaiting:
		return "awaiting you"
	case StatusInProgress:
		return "in progress"
	case StatusDone:
		return "done"
	case StatusReplied:
		return "agent replied since you finished"
	}
	return status
}

// Inbox tabs. Needs-me is the default: everything that asks the reviewer
// for something, which is everything that is not simply done.
const (
	TabNeedsMe = "needs-me"
	TabDone    = "done"
	TabAll     = "all"
)

// InTab reports whether a review with status belongs under tab. An unknown
// tab is the default one.
func InTab(status, tab string) bool {
	switch tab {
	case TabAll:
		return true
	case TabDone:
		return status == StatusDone
	}
	return status != StatusDone
}

// InboxRow is one review as the inbox lists it, and as /api/reviews reports
// it. Counts come from feedback.Materialize, never from a second tally.
type InboxRow struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Agent        string    `json:"agent,omitempty"`
	Docs         int       `json:"docs"`
	Revision     int       `json:"revision"`
	Published    time.Time `json:"published"`
	LastActivity time.Time `json:"last_activity"`
	Status       string    `json:"status"`
	StatusLabel  string    `json:"status_label"`
	Comments     int       `json:"comments"`
	Outstanding  int       `json:"outstanding"`
	Stale        int       `json:"stale"`
	Unclaimed    int       `json:"unclaimed"`
}

// Inbox is what the page renders: the rows under the chosen tab, and how
// many each tab holds.
type Inbox struct {
	Rows  []InboxRow
	Tab   string
	Theme Theme
	// Counts per tab, for the tab labels.
	NeedsMe, Done, All int
}

// inboxRow is a row as the template sees it: the row plus its display forms,
// computed here so the template stays declarative.
type inboxRow struct {
	InboxRow
	When      string // last activity, human-readable
	WhenISO   string
	Since     string // published
	SinceISO  string
	Class     string // CSS class for the status
	HasCounts bool
}

type inboxView struct {
	Rows     []inboxRow
	Tab      string
	Theme    Theme
	NeedsMe  int
	Done     int
	All      int
	TabLabel string
	Empty    string
}

// RenderInbox writes the inbox page.
func RenderInbox(w io.Writer, in Inbox) error {
	v := inboxView{Tab: in.Tab, Theme: in.Theme, NeedsMe: in.NeedsMe, Done: in.Done, All: in.All}
	switch in.Tab {
	case TabDone:
		v.TabLabel, v.Empty = "Done", "Nothing finished yet."
	case TabAll:
		v.TabLabel, v.Empty = "All", "No reviews have been published to this instance."
	default:
		v.Tab, v.TabLabel, v.Empty = TabNeedsMe, "Needs me", "Nothing is waiting for you."
	}
	for _, r := range in.Rows {
		v.Rows = append(v.Rows, inboxRow{
			InboxRow: r,
			When:     stamp(r.LastActivity), WhenISO: r.LastActivity.UTC().Format(time.RFC3339),
			Since: stamp(r.Published), SinceISO: r.Published.UTC().Format(time.RFC3339),
			Class:     "s-" + r.Status,
			HasCounts: r.Comments > 0 || r.Outstanding > 0 || r.Stale > 0 || r.Unclaimed > 0,
		})
	}
	return tmpl.ExecuteTemplate(w, "inbox.html.tmpl", v)
}

// stamp is a time as the inbox shows it: the date, and the clock when it is
// today's. No script, so no "3 hours ago" — that would be wrong the moment
// the tab was left open.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.UTC()
	if t.Format("2006-01-02") == time.Now().UTC().Format("2006-01-02") {
		return t.Format("15:04") + " UTC"
	}
	return t.Format("Jan 2, 15:04") + " UTC"
}
