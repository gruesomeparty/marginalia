package web

import (
	"io"
	"net/url"
	"strings"
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
	// Framing is the shipped preset the review was published with, "custom"
	// for an inline config alone, or empty for the default framing.
	Framing     string   `json:"framing,omitempty"`
	Names       []string `json:"documents,omitempty"`
	Status      string   `json:"status"`
	StatusLabel string   `json:"status_label"`
	Comments    int      `json:"comments"`
	Outstanding int      `json:"outstanding"`
	Stale       int      `json:"stale"`
	Unclaimed   int      `json:"unclaimed"`
}

// Ways the inbox can group its rows. Day is the default (issue 134).
const (
	GroupDay     = "day"
	GroupStatus  = "status"
	GroupFraming = "framing"
	GroupAgent   = "agent"
	GroupNone    = "none"
)

// InboxFilter is what the reviewer asked to see, as the query string carries
// it: the tab, a text search, a framing, an agent, and how to group. It lives
// in the URL rather than the page so a filtered inbox is a link, and the page
// needs no script to apply it.
type InboxFilter struct {
	Tab     string
	Query   string
	Framing string
	Agent   string
	Group   string
}

// FramingDefault and FramingCustom are the framing filter's values for a
// review with no preset and for one framed by an inline config alone.
const (
	FramingDefault = "default"
	FramingCustom  = "custom"
)

// ParseInboxFilter reads a filter from a query string. Values it does not
// know fall back to the defaults rather than failing: a stale bookmark should
// still open the inbox.
func ParseInboxFilter(q url.Values) InboxFilter {
	f := InboxFilter{
		Tab:     q.Get("tab"),
		Query:   strings.TrimSpace(q.Get("q")),
		Framing: q.Get("framing"),
		Agent:   q.Get("agent"),
		Group:   q.Get("group"),
	}
	switch f.Group {
	case GroupStatus, GroupFraming, GroupAgent, GroupNone:
	default:
		f.Group = GroupDay
	}
	return f
}

// FramingKey is a row's framing as the filter names it.
func FramingKey(framing string) string {
	if framing == "" {
		return FramingDefault
	}
	return framing
}

// Match reports whether a row passes everything but the tab: the tabs count
// what the other filters leave, so their numbers agree with the list.
func (f InboxFilter) Match(r InboxRow) bool {
	if f.Framing != "" && FramingKey(r.Framing) != f.Framing {
		return false
	}
	if f.Agent != "" && r.Agent != f.Agent {
		return false
	}
	if f.Query == "" {
		return true
	}
	q := strings.ToLower(f.Query)
	hay := []string{r.Title, r.Agent, r.Framing}
	hay = append(hay, r.Names...)
	for _, h := range hay {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

// Filtered is whether anything beyond the tab and grouping narrows the list.
func (f InboxFilter) Filtered() bool {
	return f.Query != "" || f.Framing != "" || f.Agent != ""
}

// values is the filter as a query string, tab and grouping included, with
// defaults left out so a plain inbox keeps a plain URL.
func (f InboxFilter) values() url.Values {
	v := url.Values{}
	if f.Tab != "" {
		v.Set("tab", f.Tab)
	}
	if f.Query != "" {
		v.Set("q", f.Query)
	}
	if f.Framing != "" {
		v.Set("framing", f.Framing)
	}
	if f.Agent != "" {
		v.Set("agent", f.Agent)
	}
	if f.Group != "" && f.Group != GroupDay {
		v.Set("group", f.Group)
	}
	return v
}

func (f InboxFilter) href() string {
	if e := f.values().Encode(); e != "" {
		return "/?" + e
	}
	return "/"
}

// Inbox is what the page renders: the rows under the chosen tab, and how
// many each tab holds.
type Inbox struct {
	Rows  []InboxRow
	Tab   string
	Theme Theme
	// Filter is what narrowed and grouped the rows; its Tab is ignored in
	// favour of Tab above.
	Filter InboxFilter
	// Framings and Agents are the filter menus' choices, from every review
	// rather than the filtered ones, so a choice never strands the reviewer.
	Framings, Agents []string
	// Counts per tab, for the tab labels.
	NeedsMe, Done, All int
	// Now is what "today" is measured against; zero means the clock. Tests
	// set it so a row stamped at 23:59 does not move groups mid-run.
	Now time.Time
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

// inboxGroup is a run of rows under one heading: Today, then Earlier, by last
// activity. The rows arrive newest first, so each group is contiguous.
type inboxGroup struct {
	Label string
	Rows  []inboxRow
}

// option is one choice in a filter menu.
type option struct {
	Value, Label string
	Selected     bool
}

// tabLink is a tab, carrying the rest of the filter along.
type tabLink struct {
	Key, Label, Href string
	N                int
	Current          bool
}

type inboxView struct {
	Rows   []inboxRow
	Groups []inboxGroup
	// Grouped is whether the headings show: only when both groups hold
	// something, since "Today" over every row says nothing.
	Grouped  bool
	Tab      string
	Theme    Theme
	NeedsMe  int
	Done     int
	All      int
	TabLabel string
	Empty    string
	Tabs     []tabLink
	Filter   InboxFilter
	Filtered bool
	// Clear is the current tab with no filter.
	Clear    string
	Framings []option
	Agents   []option
	GroupBy  []option
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
	f := in.Filter
	f.Tab = v.Tab
	if f.Group == "" {
		f.Group = GroupDay
	}
	v.Filter, v.Filtered = f, f.Filtered()
	v.Clear = InboxFilter{Tab: f.Tab, Group: f.Group}.href()
	if v.Filtered {
		v.Empty = "No review matches this filter."
	}
	for _, t := range []struct {
		key, label string
		n          int
	}{{TabNeedsMe, "Needs me", in.NeedsMe}, {TabDone, "Done", in.Done}, {TabAll, "All", in.All}} {
		g := f
		g.Tab = t.key
		v.Tabs = append(v.Tabs, tabLink{Key: t.key, Label: t.label, Href: g.href(), N: t.n, Current: t.key == v.Tab})
	}
	v.Framings = options(in.Framings, f.Framing, framingLabel)
	v.Agents = options(in.Agents, f.Agent, func(s string) string { return s })
	for _, g := range [][2]string{{GroupDay, "Day"}, {GroupStatus, "Status"}, {GroupFraming, "Framing"}, {GroupAgent, "Agent"}, {GroupNone, "Nothing"}} {
		v.GroupBy = append(v.GroupBy, option{Value: g[0], Label: g[1], Selected: g[0] == f.Group})
	}
	switch f.Group {
	case GroupStatus:
		v.setGroups(groupBy(v.Rows, func(r inboxRow) string { return r.Status }, statusOrder))
	case GroupFraming:
		v.setGroups(groupBy(v.Rows, func(r inboxRow) string { return framingLabel(FramingKey(r.Framing)) }, nil))
	case GroupAgent:
		v.setGroups(groupBy(v.Rows, func(r inboxRow) string {
			if r.Agent == "" {
				return "unknown agent"
			}
			return r.Agent
		}, nil))
	case GroupNone:
		v.setGroups([]inboxGroup{{Rows: v.Rows}})
	default:
		v.setGroups(groupByDay(v.Rows, in.Now))
	}
	return tmpl.ExecuteTemplate(w, "inbox.html.tmpl", v)
}

// setGroups sets the groups; headings show only when there is more than one,
// since a single heading over every row says nothing.
func (v *inboxView) setGroups(groups []inboxGroup) {
	v.Groups = groups
	v.Grouped = len(groups) > 1
}

// statusOrder puts what asks the most of the reviewer first.
var statusOrder = []string{StatusAwaiting, StatusReplied, StatusInProgress, StatusDone}

// groupBy splits rows by key, keeping each group's rows newest first. Groups
// come in order's order when one is given — a status label is written for the
// reader, not the alphabet — and otherwise by their newest row.
func groupBy(rows []inboxRow, key func(inboxRow) string, order []string) []inboxGroup {
	var keys []string
	by := map[string][]inboxRow{}
	for _, r := range rows {
		k := key(r)
		if _, ok := by[k]; !ok {
			keys = append(keys, k)
		}
		by[k] = append(by[k], r)
	}
	if order != nil {
		keys = keys[:0]
		for _, k := range order {
			if _, ok := by[k]; ok {
				keys = append(keys, k)
			}
		}
	}
	out := make([]inboxGroup, 0, len(keys))
	for _, k := range keys {
		label := k
		if order != nil {
			label = StatusLabel(k)
		}
		out = append(out, inboxGroup{Label: label, Rows: by[k]})
	}
	return out
}

// framingLabel is a framing filter value as the menu shows it.
func framingLabel(key string) string {
	switch key {
	case FramingDefault:
		return "default framing"
	case FramingCustom:
		return "custom framing"
	}
	return key
}

// options is a filter menu: "any", then each choice, with the current one
// selected. A current value no review carries any more is still listed, so
// the menu never silently shows a different filter from the one applied.
func options(choices []string, current string, label func(string) string) []option {
	out := []option{{Value: "", Label: "any", Selected: current == ""}}
	seen := false
	for _, c := range choices {
		out = append(out, option{Value: c, Label: label(c), Selected: c == current})
		seen = seen || c == current
	}
	if current != "" && !seen {
		out = append(out, option{Value: current, Label: label(current), Selected: true})
	}
	return out
}

// groupByDay splits rows into Today and Earlier by last activity, in UTC,
// the zone every stamp on the page is written in. An empty group is left out.
func groupByDay(rows []inboxRow, now time.Time) []inboxGroup {
	if now.IsZero() {
		now = time.Now()
	}
	today := now.UTC().Format("2006-01-02")
	var recent, earlier []inboxRow
	for _, r := range rows {
		if r.LastActivity.UTC().Format("2006-01-02") == today {
			recent = append(recent, r)
		} else {
			earlier = append(earlier, r)
		}
	}
	var out []inboxGroup
	if len(recent) > 0 {
		out = append(out, inboxGroup{Label: "Today", Rows: recent})
	}
	if len(earlier) > 0 {
		out = append(out, inboxGroup{Label: "Earlier", Rows: earlier})
	}
	return out
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
