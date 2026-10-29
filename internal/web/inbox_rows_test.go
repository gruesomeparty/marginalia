package web

import (
	"bytes"
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
