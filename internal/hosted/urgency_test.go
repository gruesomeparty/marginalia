package hosted

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// The acceptance criterion of #220 (a): of three reviews, the blocking one
// and the one past its due-by list first, with how long the agent has
// waited and an overdue marker; the plain one, though newer, comes after;
// /api/reviews says the same; and once the reviewer is done neither is
// urgent any more.
func TestBlockingAndOverdueReviewsListFirst(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	h, ts := openClocked(t, storage.NewMemory(), &clock)
	ctx := context.Background()
	docs := func() []Document { return []Document{{Name: "spec.md", Content: []byte(specDoc)}} }
	blocking, err := h.Publish(ctx, Publish{Title: "blocking one", Agent: "agent:a", Docs: docs(), Blocking: true})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	overdue, err := h.Publish(ctx, Publish{Title: "overdue one", Agent: "agent:b", Docs: docs(), DueBy: clock.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	plain, err := h.Publish(ctx, Publish{Title: "plain one", Agent: "agent:c", Docs: docs()})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(3 * time.Hour) // the due-by passes

	code, body := get(t, ts+"/")
	if code != 200 {
		t.Fatalf("GET / = %d", code)
	}
	var ids []string
	for _, m := range regexp.MustCompile(`data-review="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		ids = append(ids, m[1])
	}
	want := []string{overdue.Meta.ID, blocking.Meta.ID, plain.Meta.ID}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want overdue, blocking, plain %v", ids, want)
	}
	for _, s := range []string{"An agent is waiting", ">blocking<", ">overdue<", "agent waiting 3h 2m", "agent waiting 3h 1m", "due Oct 14, 09:31 UTC"} {
		if !strings.Contains(body, s) {
			t.Errorf("the inbox does not say %q", s)
		}
	}

	code, body = get(t, ts+"/api/reviews")
	if code != 200 {
		t.Fatalf("GET /api/reviews = %d", code)
	}
	var rows []web.InboxRow
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	by := map[string]web.InboxRow{}
	for _, r := range rows {
		by[r.ID] = r
	}
	if r := by[blocking.Meta.ID]; !r.Blocking || !r.Urgent || r.Overdue || r.DueBy != nil {
		t.Errorf("blocking row = %+v", r)
	}
	if r := by[overdue.Meta.ID]; r.Blocking || !r.Urgent || !r.Overdue || r.DueBy == nil || !r.DueBy.Equal(time.Date(2026, 10, 14, 9, 31, 0, 0, time.UTC)) {
		t.Errorf("overdue row = %+v", r)
	}
	if r := by[plain.Meta.ID]; r.Blocking || r.Urgent || r.Overdue {
		t.Errorf("plain row = %+v", r)
	}

	// The reviewer finishes the blocking one: it is just done now.
	if err := blocking.Server.Entries()[0].Store.Append(feedback.Event{Doc: "spec.md", Type: feedback.TypeReviewDone, Author: "dev@example.com", Ts: clock.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if r := h.row(blocking); r.Urgent || r.Status != web.StatusDone {
		t.Errorf("a finished blocking review is still urgent: %+v", r)
	}
	// The agent answers after Done without republishing: replied, and still
	// not the reviewer's to wait on.
	events, _ := blocking.Server.Entries()[0].Store.Load()
	clock = clock.Add(time.Minute)
	if err := blocking.Server.Entries()[0].Store.Append(feedback.Event{Doc: "spec.md", Type: feedback.TypeReply, Text: "noted", Author: "agent:a", ReplyTo: feedback.NoteID(events[0]), Ts: clock.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if r := h.row(blocking); r.Status != web.StatusReplied || r.Urgent || r.Blocking {
		t.Errorf("an agent's reply after Done made a blocking review urgent again: %+v", r)
	}
}

// A republish states its own urgency: one that drops blocking is no longer
// blocking, and the wait restarts from the new revision.
func TestARepublishStatesItsOwnUrgency(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	h, _ := openClocked(t, storage.NewMemory(), &clock)
	ctx := context.Background()
	r, err := h.Publish(ctx, Publish{Title: "t", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}, Blocking: true})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	r, err = h.Republish(ctx, r.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte(specDoc + "\nmore\n")}}})
	if err != nil {
		t.Fatal(err)
	}
	if row := h.row(r); row.Blocking || row.Urgent || !row.WaitingSince.Equal(clock) {
		t.Errorf("after a plain republish: %+v", row)
	}
}

// A blocking publish is not held back by the debounce: it goes at once,
// and says the agent is waiting.
func TestABlockingPublishSkipsTheDebounce(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	rc := newReceiver(t)
	h, _, _ := openNotifying(t, rc, "text", &clock)
	ctx := context.Background()
	r, err := h.Publish(ctx, Publish{Title: "spec", Agent: "a", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := rc.wait(1); len(got) != 1 {
		t.Fatalf("the first publish: %d POSTs", len(got))
	}
	clock = clock.Add(5 * time.Second)
	if _, err := h.Republish(ctx, r.Meta.ID, Publish{Docs: []Document{{Name: "spec.md", Content: []byte(specDoc + "\nx\n")}}, Blocking: true}); err != nil {
		t.Fatal(err)
	}
	posts := rc.wait(2)
	if len(posts) != 2 {
		t.Fatalf("a blocking republish inside the window: %d POSTs, want 2", len(posts))
	}
	if !strings.Contains(posts[1].body, "blocking") {
		t.Errorf("the notification does not say it is blocking: %q", posts[1].body)
	}
}

// Over MCP, blocking and due_by reach the review the inbox reads.
func TestPublishToolTakesUrgency(t *testing.T) {
	h, ts := open(t, storage.NewMemory())
	cs := connect(t, ts)
	var out struct {
		ReviewID string `json:"review_id"`
	}
	if err := call(t, cs, "publish_document", map[string]any{
		"documents": []map[string]any{{"name": "a.md", "content": "# a\n"}},
		"blocking":  true, "due_by": "2026-11-01T17:00:00+01:00",
	}, &out); err != nil {
		t.Fatal(err)
	}
	r := h.Review(out.ReviewID)
	if r == nil || !r.Meta.Blocking || r.Meta.DueBy != "2026-11-01T16:00:00Z" {
		t.Fatalf("meta = %+v", r)
	}
}
