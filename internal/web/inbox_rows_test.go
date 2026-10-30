package web

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func renderInbox(t *testing.T, now time.Time, rows ...InboxRow) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderInbox(&buf, Inbox{Rows: rows, Tab: TabAll, All: len(rows), Now: now}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func row(id, status string, last time.Time, outstanding int) InboxRow {
	return InboxRow{
		ID: id, URL: "/r/" + id + "/", Title: id, Status: status, StatusLabel: StatusLabel(status),
		Published: last.Add(-time.Hour), LastActivity: last, Outstanding: outstanding, Comments: outstanding, Docs: 1, Revision: 1,
	}
}

// Each row leads with its status, then what is outstanding (issue 134).
func TestInboxRowLeadsWithStatus(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	out := renderInbox(t, now, row("a", StatusInProgress, now, 3))
	first := regexp.MustCompile(`data-status="[^"]+">\s*<span class="lead"><span class="status">([^<]+)</span><span class="need">3 outstanding</span>`)
	m := first.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("row does not open with its status and outstanding count:\n%s", out)
	}
	if m[1] != StatusLabel(StatusInProgress) {
		t.Errorf("status chip reads %q", m[1])
	}
}

// Today/Earlier headings appear only when both groups hold a row.
func TestInboxGroupsByDayOnlyWhenBothExist(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	today := row("today", StatusAwaiting, now.Add(-time.Hour), 0)
	old := row("old", StatusDone, now.Add(-48*time.Hour), 0)

	both := renderInbox(t, now, today, old)
	if !strings.Contains(both, "<h2>Today</h2>") || !strings.Contains(both, "<h2>Earlier</h2>") {
		t.Errorf("both groups have rows, but the headings are missing")
	}
	if strings.Index(both, `data-review="today"`) > strings.Index(both, "<h2>Earlier</h2>") {
		t.Errorf("today's row is filed under Earlier")
	}
	for name, out := range map[string]string{
		"only today":   renderInbox(t, now, today),
		"only earlier": renderInbox(t, now, old),
	} {
		if strings.Contains(out, "<h2>") {
			t.Errorf("%s: a heading shows over a single group", name)
		}
	}
}

func renderFiltered(t *testing.T, in Inbox) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderInbox(&buf, in); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// Search covers title, agent, framing and document names, case-blind; the
// framing and agent filters are exact.
func TestInboxFilterMatch(t *testing.T) {
	r := InboxRow{Title: "Payment API", Agent: "agent:ci", Framing: "schema", Names: []string{"orders.proto"}}
	for _, c := range []struct {
		f    InboxFilter
		want bool
	}{
		{InboxFilter{}, true},
		{InboxFilter{Query: "payment"}, true},
		{InboxFilter{Query: "ORDERS"}, true},
		{InboxFilter{Query: "ci"}, true},
		{InboxFilter{Query: "billing"}, false},
		{InboxFilter{Framing: "schema"}, true},
		{InboxFilter{Framing: FramingDefault}, false},
		{InboxFilter{Agent: "agent:ci"}, true},
		{InboxFilter{Agent: "agent:other"}, false},
	} {
		if got := c.f.Match(r); got != c.want {
			t.Errorf("%+v: match = %v, want %v", c.f, got, c.want)
		}
	}
	if !(InboxFilter{Framing: FramingDefault}).Match(InboxRow{}) {
		t.Errorf("a review with no preset is not under the default framing")
	}
}

// An unknown grouping is the default one, so a stale link still opens.
func TestParseInboxFilterDefaults(t *testing.T) {
	f := ParseInboxFilter(url.Values{"group": {"bogus"}, "q": {"  spec "}})
	if f.Group != GroupDay || f.Query != "spec" {
		t.Errorf("parsed %+v", f)
	}
}

// Grouping by status puts what asks the most of the reviewer first, and the
// tabs keep the rest of the filter when switching.
func TestInboxGroupsByStatus(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rows := []InboxRow{
		row("prog", StatusInProgress, now, 1),
		row("wait", StatusAwaiting, now.Add(-time.Hour), 0),
		row("back", StatusReplied, now.Add(-2*time.Hour), 0),
	}
	out := renderFiltered(t, Inbox{
		Rows: rows, Tab: TabNeedsMe, NeedsMe: 3, All: 3, Now: now,
		Filter: InboxFilter{Group: GroupStatus, Query: "x"},
	})
	var heads []string
	for _, m := range regexp.MustCompile(`<h2>([^<]+)</h2>`).FindAllStringSubmatch(out, -1) {
		heads = append(heads, m[1])
	}
	want := []string{StatusLabel(StatusAwaiting), StatusLabel(StatusReplied), StatusLabel(StatusInProgress)}
	if strings.Join(heads, "|") != strings.Join(want, "|") {
		t.Errorf("headings %q, want %q", heads, want)
	}
	if !strings.Contains(out, `href="/?group=status&amp;q=x&amp;tab=done"`) {
		t.Errorf("the Done tab drops the filter:\n%s", out)
	}
	if !strings.Contains(out, `class="clear" href="/?group=status&amp;tab=needs-me"`) {
		t.Errorf("no clear link back to the unfiltered tab")
	}
	if !strings.Contains(out, `<option value="status" selected>`) {
		t.Errorf("the grouping menu does not show the grouping applied")
	}
}

// A framing filter that matches nothing any more still shows in its menu, and
// the empty list says it is the filter, not the inbox, that is empty.
func TestInboxKeepsAStaleChoice(t *testing.T) {
	out := renderFiltered(t, Inbox{Tab: TabAll, Framings: []string{FramingDefault}, Filter: InboxFilter{Framing: "adr"}})
	if !strings.Contains(out, `<option value="adr" selected>adr</option>`) {
		t.Errorf("the applied framing is missing from the menu")
	}
	if !strings.Contains(out, "No review matches this filter.") {
		t.Errorf("empty filtered inbox reads as an empty instance")
	}
}

// A status the order does not list still gets a group, after the listed ones.
func TestGroupByKeepsAnUnlistedStatus(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rows := []inboxRow{{InboxRow: row("new", "archived", now, 0)}, {InboxRow: row("wait", StatusAwaiting, now, 0)}}
	groups := groupBy(rows, func(r inboxRow) string { return r.Status }, statusOrder)
	if len(groups) != 2 || groups[0].Label != StatusLabel(StatusAwaiting) || groups[1].Label != "archived" {
		t.Errorf("groups = %+v", groups)
	}
}
