package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// seedInbox publishes four reviews, one in each state, with last activity
// in a known order: replied (newest), in progress, awaiting, done (oldest).
// The clock is injected so the order is the test's, not the scheduler's.
func seedInbox(t *testing.T, h *Host, base string, clock *time.Time) map[string]*Review {
	t.Helper()
	ctx := context.Background()
	tick := func() string {
		*clock = clock.Add(time.Minute)
		return clock.UTC().Format(time.RFC3339)
	}
	out := map[string]*Review{}
	appendTo := func(r *Review, e feedback.Event) {
		t.Helper()
		e.Doc = "spec.md"
		if e.Ts == "" {
			e.Ts = tick()
		}
		// The page sends the block's hash with a note; so does this.
		if e.Block != "" && e.Hash == "" {
			for _, b := range r.Server.Entries()[0].Doc.Blocks {
				if b.ID == e.Block {
					e.Hash = b.Hash
				}
			}
		}
		if err := r.Server.Entries()[0].Store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	// done: a note, then review_done, long ago.
	done, err := h.Publish(ctx, Publish{Title: "done one", Agent: "agent:a", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	appendTo(done, feedback.Event{Block: "1/2", Type: "approve", Author: "berkay"})
	appendTo(done, feedback.Event{Type: feedback.TypeReviewDone, Author: "berkay"})
	out[web.StatusDone] = done
	// awaiting: published, nothing said. Its publish is its last activity,
	// so it comes after the done one's Done.
	*clock = clock.Add(time.Minute)
	awaiting, err := h.Publish(ctx, Publish{Title: "awaiting one", Agent: "agent:b", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	out[web.StatusAwaiting] = awaiting
	// in progress: a note, no Done.
	*clock = clock.Add(time.Minute)
	progress, err := h.Publish(ctx, Publish{Title: "in progress one", Agent: "agent:c", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	appendTo(progress, feedback.Event{Block: "1/2", Type: "comment", Text: "why?", Author: "berkay"})
	out[web.StatusInProgress] = progress
	// replied: done, then the agent answered.
	*clock = clock.Add(time.Minute)
	replied, err := h.Publish(ctx, Publish{Title: "replied one", Agent: "agent:d", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	appendTo(replied, feedback.Event{Block: "1/2", Type: "question", Text: "why?", Author: "berkay"})
	appendTo(replied, feedback.Event{Type: feedback.TypeReviewDone, Author: "berkay"})
	events, _ := replied.Server.Entries()[0].Store.Load()
	appendTo(replied, feedback.Event{Block: "1/2", Type: feedback.TypeReply, Text: "because", Author: "agent:d", ReplyTo: feedback.NoteID(events[0])})
	out[web.StatusReplied] = replied
	_ = base
	return out
}

func openClocked(t *testing.T, store storage.Store, clock *time.Time) (*Host, string) {
	t.Helper()
	h, err := Open(context.Background(), Options{
		Store: store, PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com",
		Log: testWriter{t}, Now: func() time.Time { return *clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return h, ts.URL
}

// The acceptance criterion of #99: four reviews, one per state, each under
// the right tab with the right label, newest activity first; the JSON agrees;
// rendering costs the store nothing; an agent credential is refused; and the
// tokens test covers the inbox styles (internal/web).
func TestInboxFilesEveryReviewUnderTheRightTab(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	counting := storagetest.NewCounting(storage.NewMemory())
	h, ts := openClocked(t, counting, &clock)
	reviews := seedInbox(t, h, ts, &clock)
	counting.Reset()

	order := func(body string) []string {
		var ids []string
		for _, m := range regexp.MustCompile(`data-review="([^"]+)"`).FindAllStringSubmatch(body, -1) {
			ids = append(ids, m[1])
		}
		return ids
	}
	labels := func(body string) map[string]string {
		got := map[string]string{}
		for _, m := range regexp.MustCompile(`data-review="([^"]+)" data-status="([^"]+)"`).FindAllStringSubmatch(body, -1) {
			got[m[1]] = m[2]
		}
		return got
	}
	want := map[string][]string{
		"":          {web.StatusReplied, web.StatusInProgress, web.StatusAwaiting},
		"?tab=done": {web.StatusDone},
		"?tab=all":  {web.StatusReplied, web.StatusInProgress, web.StatusAwaiting, web.StatusDone},
	}
	for query, statuses := range want {
		code, body := get(t, ts+"/"+query)
		if code != 200 {
			t.Fatalf("GET /%s = %d", query, code)
		}
		var wantIDs []string
		for _, s := range statuses {
			wantIDs = append(wantIDs, reviews[s].Meta.ID)
		}
		if got := order(body); strings.Join(got, ",") != strings.Join(wantIDs, ",") {
			t.Errorf("GET /%s lists %v, want %v (newest activity first)", query, got, wantIDs)
		}
		for id, status := range labels(body) {
			if reviews[status] == nil || reviews[status].Meta.ID != id {
				t.Errorf("GET /%s files %s as %s", query, id, status)
			}
			if !strings.Contains(body, web.StatusLabel(status)) {
				t.Errorf("GET /%s does not show the label %q", query, web.StatusLabel(status))
			}
		}
		// The tab counts are on the page.
		if !strings.Contains(body, `Needs me<span class="n">3</span>`) || !strings.Contains(body, `Done<span class="n">1</span>`) || !strings.Contains(body, `All<span class="n">4</span>`) {
			t.Errorf("GET /%s tab counts are wrong", query)
		}
	}
	// The JSON agrees with the HTML on every status.
	code, body := get(t, ts+"/api/reviews")
	if code != 200 {
		t.Fatalf("/api/reviews = %d", code)
	}
	var rows []web.InboxRow
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("/api/reviews has %d rows", len(rows))
	}
	for i, r := range rows {
		if reviews[r.Status].Meta.ID != r.ID {
			t.Errorf("/api/reviews files %s as %s", r.ID, r.Status)
		}
		if r.StatusLabel != web.StatusLabel(r.Status) || !strings.HasPrefix(r.URL, "https://review.example/r/") {
			t.Errorf("row %+v", r)
		}
		if i > 0 && rows[i-1].LastActivity.Before(r.LastActivity) {
			t.Errorf("/api/reviews is not newest first at %d", i)
		}
	}
	// Counts come from Materialize: the in-progress one has an outstanding
	// note, the replied one a note that was answered.
	byStatus := map[string]web.InboxRow{}
	for _, r := range rows {
		byStatus[r.Status] = r
	}
	if r := byStatus[web.StatusInProgress]; r.Comments != 1 || r.Outstanding != 1 {
		t.Errorf("in progress counts: %+v", r)
	}
	if r := byStatus[web.StatusAwaiting]; r.Comments != 0 || r.Outstanding != 0 {
		t.Errorf("awaiting counts: %+v", r)
	}
	if r := byStatus[web.StatusDone]; r.Agent != "agent:a" || r.Revision != 1 || r.Docs != 1 || r.Title != "done one" {
		t.Errorf("done row: %+v", r)
	}
	// And none of that touched the store.
	if got := counting.Reads(); got != 0 {
		t.Errorf("rendering the inbox cost %d store reads after startup, want 0", got)
	}
	// An agent credential on the inbox is refused, on both forms.
	for _, path := range []string{"/", "/api/reviews"} {
		if code, _ := do(t, "GET", ts+path, nil, map[string]string{"Authorization": "Bearer tok"}); code != http.StatusForbidden {
			t.Errorf("agent credential on %s: %d, want 403", path, code)
		}
	}
}

// A revision moves a review up the list and a stale count appears; a human
// note on a done review puts it back in progress; a title-less review is
// named after its first document.
func TestInboxFollowsTheLoop(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	h, ts := openClocked(t, storage.NewMemory(), &clock)
	reviews := seedInbox(t, h, ts, &clock)
	done := reviews[web.StatusDone]
	// The oldest review gets a revision: it is now the newest activity, and
	// its approved block changed, so the note reads stale.
	clock = clock.Add(time.Hour)
	if _, err := h.Republish(context.Background(), done.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte("# Spec\n\nChanged.\n")}}}); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, ts+"/api/reviews")
	var rows []web.InboxRow
	_ = json.Unmarshal([]byte(body), &rows)
	if rows[0].ID != done.Meta.ID || rows[0].Revision != 2 || rows[0].Stale != 1 || rows[0].Status != web.StatusDone {
		t.Errorf("after a revision: %+v", rows[0])
	}
	// A note the reviewer adds after Done is activity, and the counts
	// move; the review stays done — Done was pressed — until the agent
	// answers, which is what brings it back to the needs-me tab.
	e := feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "hm", Author: "berkay", Ts: clock.Add(2 * time.Hour).UTC().Format(time.RFC3339)}
	if err := h.Review(done.Meta.ID).Server.Entries()[0].Store.Append(e); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, ts+"/api/reviews?tab=done")
	_ = json.Unmarshal([]byte(body), &rows)
	if rows[0].ID != done.Meta.ID || rows[0].Status != web.StatusDone || rows[0].Comments != 2 || !rows[0].LastActivity.Equal(clock.Add(2*time.Hour)) {
		t.Errorf("after a new note on a done review: %+v", rows[0])
	}
	events, _ := h.Review(done.Meta.ID).Server.Entries()[0].Store.Load()
	reply := feedback.Event{Doc: "spec.md", Block: "1/2", Type: feedback.TypeReply, Text: "ok", Author: "agent:a", ReplyTo: feedback.NoteID(events[len(events)-1]), Ts: clock.Add(3 * time.Hour).UTC().Format(time.RFC3339)}
	if err := h.Review(done.Meta.ID).Server.Entries()[0].Store.Append(reply); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, ts+"/api/reviews?tab=needs-me")
	_ = json.Unmarshal([]byte(body), &rows)
	if rows[0].ID != done.Meta.ID || rows[0].Status != web.StatusReplied {
		t.Errorf("after the agent replied on a done review: %+v", rows[0])
	}
	// No title: the first document names it.
	untitled, err := h.Publish(context.Background(), Publish{Docs: []Document{{Name: "plan.md", Content: []byte(planDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, body = get(t, ts+"/")
	if !strings.Contains(body, `data-review="`+untitled.Meta.ID+`"`) || !strings.Contains(body, `<span class="title">plan.md</span>`) {
		t.Error("an untitled review is not named after its document")
	}
}

func TestInboxIsEmptyBeforeAnythingIsPublished(t *testing.T) {
	_, ts := open(t, storage.NewMemory())
	code, body := get(t, ts.URL+"/")
	if code != 200 || !strings.Contains(body, "Nothing is waiting for you.") || strings.Contains(body, "<script src") {
		t.Errorf("empty inbox: %d", code)
	}
	code, body = get(t, ts.URL+"/api/reviews")
	if code != 200 || strings.TrimSpace(body) != "[]" {
		t.Errorf("empty /api/reviews: %d %q", code, body)
	}
}

// Search, framing and agent narrow the inbox and its JSON alike, and the tab
// counts follow the filter so they agree with the list.
func TestInboxFilters(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	h, ts := openClocked(t, storage.NewMemory(), &clock)
	reviews := seedInbox(t, h, ts, &clock)
	adr, err := h.Publish(context.Background(), Publish{
		Title: "Decision record", Agent: "agent:a", Review: "adr",
		Docs: []Document{{Name: "decision.md", Content: []byte(specDoc)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(body string) []string {
		var out []string
		for _, m := range regexp.MustCompile(`data-review="([^"]+)"`).FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return out
	}
	for query, want := range map[string][]string{
		"?tab=all&q=progress":       {reviews[web.StatusInProgress].Meta.ID},
		"?tab=all&q=DECISION.MD":    {adr.Meta.ID},
		"?tab=all&framing=adr":      {adr.Meta.ID},
		"?tab=all&agent=agent%3Aa":  {adr.Meta.ID, reviews[web.StatusDone].Meta.ID},
		"?tab=done&agent=agent%3Aa": {reviews[web.StatusDone].Meta.ID},
	} {
		_, body := get(t, ts+"/"+query)
		if got := ids(body); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("GET /%s lists %v, want %v", query, got, want)
		}
		_, js := get(t, ts+"/api/reviews"+query)
		var rows []web.InboxRow
		if err := json.Unmarshal([]byte(js), &rows); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rows {
			got = append(got, r.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("/api/reviews%s lists %v, want %v", query, got, want)
		}
	}
	_, body := get(t, ts+"/?agent=agent%3Aa")
	if !strings.Contains(body, `Needs me<span class="n">1</span>`) || !strings.Contains(body, `Done<span class="n">1</span>`) || !strings.Contains(body, `All<span class="n">2</span>`) {
		t.Errorf("tab counts ignore the filter")
	}
	for _, opt := range []string{`<option value="adr">adr</option>`, `<option value="default">default framing</option>`, `<option value="agent:d">agent:d</option>`} {
		if !strings.Contains(body, opt) {
			t.Errorf("the filter menus lack %s", opt)
		}
	}
}
