package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
)

// openLimited is a host with small caps and an injected clock.
func openLimited(t *testing.T, store storage.Store, limits Limits, clock *time.Time) (*Host, *httptest.Server) {
	t.Helper()
	h, err := Open(context.Background(), Options{
		Store: store, PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com",
		Log: testWriter{t}, Now: func() time.Time { return *clock }, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return h, ts
}

func doc(name string, size int) Document {
	return Document{Name: name, Content: append([]byte("# T\n\n"), bytes.Repeat([]byte("x"), size)...)}
}

// Every cap refuses with a message naming the limit and its value, and
// writes nothing.
func TestEveryLimitRefusesByNameBeforeWriting(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	counting := storagetest.NewCounting(storage.NewMemory())
	h, _ := openLimited(t, counting, Limits{DocumentBytes: 100, DocumentsPerPublish: 2, RevisionsPerReview: 2, PublishesPerHour: 3, OpenReviews: 2, StoredBytes: 500}, &clock)
	ctx := context.Background()

	refuse := func(name, limit string, p Publish, reviewID string) {
		t.Helper()
		before := counting.Writes()
		var err error
		if reviewID == "" {
			_, err = h.Publish(ctx, p)
		} else {
			_, err = h.Republish(ctx, reviewID, p)
		}
		var le *LimitError
		if !errors.As(err, &le) {
			t.Fatalf("%s: got %v, want a limit error", name, err)
		}
		if !strings.Contains(err.Error(), limit) {
			t.Errorf("%s: %v does not name %q", name, err, limit)
		}
		if got := counting.Writes(); got != before {
			t.Errorf("%s: %d write(s) reached the store", name, got-before)
		}
	}

	refuse("too large", "bytes per document is 100", Publish{Agent: "a", Docs: []Document{doc("big.md", 200)}}, "")
	refuse("too many documents", "documents per publish is 2", Publish{Agent: "a", Docs: []Document{doc("a.md", 1), doc("b.md", 1), doc("c.md", 1)}}, "")

	r, err := h.Publish(ctx, Publish{Agent: "a", Docs: []Document{doc("a.md", 10)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Republish(ctx, r.Meta.ID, Publish{Docs: []Document{doc("a.md", 11)}}); err != nil {
		t.Fatal(err)
	}
	refuse("too many revisions", "revisions per review is 2", Publish{Docs: []Document{doc("a.md", 12)}}, r.Meta.ID)

	// Open reviews: one open already; a second is fine, a third is not.
	if _, err := h.Publish(ctx, Publish{Agent: "b", Docs: []Document{doc("b.md", 10)}}); err != nil {
		t.Fatal(err)
	}
	refuse("too many open", "open reviews is 2", Publish{Agent: "c", Docs: []Document{doc("c.md", 10)}}, "")
	// Marking one done makes room, and the limit only counts open ones.
	if err := r.Server.Entries()[0].Store.Append(feedback.Event{Doc: "a.md", Type: feedback.TypeReviewDone, Author: "dev", Ts: clock.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Publish(ctx, Publish{Agent: "c", Docs: []Document{doc("c.md", 10)}}); err != nil {
		t.Fatalf("a done review still counts as open: %v", err)
	}

	// Stored bytes: the budget is 500. Finish the open reviews first so the
	// open cap is out of the way, then a document that would push the total
	// past the budget is refused by name.
	for _, name := range []string{"b.md", "c.md"} {
		for _, r := range h.Reviews() {
			if r.Meta.Docs[0] == name {
				if err := r.Server.Entries()[0].Store.Append(feedback.Event{Doc: name, Type: feedback.TypeReviewDone, Author: "dev", Ts: clock.Format(time.RFC3339)}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if h.openReviews() != 0 {
		t.Fatalf("%d reviews still open", h.openReviews())
	}
	if h.Stored() >= 500 {
		t.Fatalf("stored = %d before the budget case; the test needs headroom", h.Stored())
	}
	refuse("over budget", "stored bytes is 500", Publish{Agent: "d", Docs: []Document{doc("d.md", int(500-h.Stored()))}}, "")
}

// The rate limit counts an agent's publishes in a sliding hour and resets as
// the window moves. A refused publish does not count.
func TestPublishesPerHourSlideWithTheClock(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	h, _ := openLimited(t, storage.NewMemory(), Limits{PublishesPerHour: 2, OpenReviews: 100}, &clock)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := h.Publish(ctx, Publish{Agent: "agent:a", Docs: []Document{doc("a.md", 10)}}); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(10 * time.Minute)
	}
	_, err := h.Publish(ctx, Publish{Agent: "agent:a", Docs: []Document{doc("a.md", 10)}})
	if err == nil || !strings.Contains(err.Error(), "publishes per agent per hour is 2") {
		t.Fatalf("third publish in the hour: %v", err)
	}
	// Another agent is not affected.
	if _, err := h.Publish(ctx, Publish{Agent: "agent:b", Docs: []Document{doc("b.md", 10)}}); err != nil {
		t.Fatalf("another agent was rate-limited: %v", err)
	}
	// The first publish was 20 minutes ago; 41 minutes later it is out of
	// the window and one slot is free again.
	clock = clock.Add(41 * time.Minute)
	if _, err := h.Publish(ctx, Publish{Agent: "agent:a", Docs: []Document{doc("a.md", 10)}}); err != nil {
		t.Fatalf("the window did not slide: %v", err)
	}
	if _, err := h.Publish(ctx, Publish{Agent: "agent:a", Docs: []Document{doc("a.md", 10)}}); err == nil {
		t.Fatal("a fourth publish inside the window was accepted")
	}
}

// A human's note is never refused by the byte budget, and still counts
// towards it.
func TestTheBudgetNeverRefusesAHumanNote(t *testing.T) {
	clock := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	h, ts := openLimited(t, storage.NewMemory(), Limits{StoredBytes: 60}, &clock)
	r, err := h.Publish(context.Background(), Publish{Agent: "a", Docs: []Document{doc("a.md", 50)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Publish(context.Background(), Publish{Agent: "a", Docs: []Document{doc("b.md", 50)}}); err == nil {
		t.Fatal("the budget did not refuse a publish")
	}
	before := h.Stored()
	body, _ := json.Marshal(feedback.Event{Doc: "a.md", Block: "1/1", Type: "comment", Text: strings.Repeat("a long note ", 50)})
	res, err := http.Post(ts.URL+"/r/"+r.Meta.ID+"/api/feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("a human's note was refused with the budget exhausted: %d", res.StatusCode)
	}
	if h.Stored() <= before {
		t.Errorf("the note did not count against the budget: %d → %d", before, h.Stored())
	}
}

// The sweep archives what outlived its retention — done reviews after 30
// days, open ones after 90 — leaves the recently active alone, moves whole
// reviews with their logs byte-identical, and takes them out of the inbox.
func TestSweepArchivesByRetention(t *testing.T) {
	clock := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	store := storage.NewMemory()
	h, ts := openLimited(t, store, Limits{OpenReviews: 100}, &clock)
	ctx := context.Background()
	pub := func(title string) *Review {
		t.Helper()
		r, err := h.Publish(ctx, Publish{Title: title, Agent: "a", Docs: []Document{doc("a.md", 10)}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	finish := func(r *Review) {
		t.Helper()
		for _, e := range []feedback.Event{
			{Doc: "a.md", Block: "1/1", Type: "comment", Text: "n", Author: "dev", Ts: clock.Format(time.RFC3339)},
			{Doc: "a.md", Type: feedback.TypeReviewDone, Author: "dev", Ts: clock.Add(time.Minute).Format(time.RFC3339)},
		} {
			if err := r.Server.Entries()[0].Store.Append(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	oldDone := pub("old done")
	finish(oldDone)
	oldOpen := pub("old open")
	clock = clock.Add(31 * 24 * time.Hour)
	recentDone := pub("recent done")
	finish(recentDone)
	recentOpen := pub("recent open")

	// Day 31: the old done one is past 30 days; the old open one is not
	// past 90.
	archived, err := h.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 1 || archived[0] != oldDone.Meta.ID {
		t.Fatalf("day 31 archived %v, want only the old done review", archived)
	}
	// Moved whole: the log reads byte-identical under archive/, nothing is
	// left under reviews/, and the inbox no longer lists it.
	lines, err := store.ReadLog(ctx, "archive/"+oldDone.Meta.ID+"/log/a.md")
	if err != nil || len(lines) != 2 || !strings.Contains(string(lines[1]), `"type":"review_done"`) {
		t.Errorf("archived log: %q, %v", lines, err)
	}
	if left, _ := store.List(ctx, "reviews/"+oldDone.Meta.ID+"/"); len(left) != 0 {
		t.Errorf("keys left under reviews/: %v", left)
	}
	if blob, err := store.GetBlob(ctx, "archive/"+oldDone.Meta.ID+"/rev/1/a.md"); err != nil || !bytes.Equal(blob, doc("a.md", 10).Content) {
		t.Errorf("archived document: %v", err)
	}
	if _, body := get(t, ts.URL+"/api/reviews?tab=all"); strings.Contains(body, oldDone.Meta.ID) {
		t.Error("an archived review is still in the inbox")
	}
	if code, _ := get(t, ts.URL+"/r/"+oldDone.Meta.ID+"/"); code != http.StatusNotFound {
		t.Errorf("an archived review is still served: %d", code)
	}

	// Day 91 (from the start): the old open one is past 90 days, and the
	// recent done one — finished 60 days ago — is past 30; the recent open
	// one, 60 days old, is not past 90.
	clock = clock.Add(60 * 24 * time.Hour)
	archived, err = h.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, id := range archived {
		got[id] = true
	}
	if len(got) != 2 || !got[oldOpen.Meta.ID] || !got[recentDone.Meta.ID] {
		t.Fatalf("day 91 archived %v, want the old open and the recent done reviews", archived)
	}
	if h.Review(recentOpen.Meta.ID) == nil {
		t.Error("the recent open review was archived too early")
	}
	// A sweep with nothing to do does nothing.
	if archived, _ := h.Sweep(ctx); len(archived) != 0 {
		t.Errorf("a third sweep archived %v", archived)
	}
	// A restart on the same store does not bring archived reviews back.
	ts.Close()
	h2, _ := openLimited(t, store, Limits{OpenReviews: 100}, &clock)
	if got := len(h2.Reviews()); got != 1 {
		t.Errorf("after restart: %d reviews, want 1", got)
	}
}

func TestStoredBytesSurviveRestart(t *testing.T) {
	clock := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	store := storage.NewMemory()
	h, ts := openLimited(t, store, Limits{}, &clock)
	r, err := h.Publish(context.Background(), Publish{Agent: "a", Docs: []Document{doc("a.md", 100)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Republish(context.Background(), r.Meta.ID, Publish{Docs: []Document{doc("a.md", 200)}}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(feedback.Event{Doc: "a.md", Block: "1/1", Type: "comment", Text: "note"})
	res, err := http.Post(ts.URL+"/r/"+r.Meta.ID+"/api/feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	before := h.Stored()
	if before < 300 {
		t.Fatalf("stored = %d, want at least both revisions", before)
	}
	ts.Close()
	h2, _ := openLimited(t, store, Limits{}, &clock)
	if h2.Stored() != before {
		t.Errorf("stored after restart = %d, want %d: the total must come from metadata and the warm read, not a listing", h2.Stored(), before)
	}
}

func TestLimitsDefaultWhereUnset(t *testing.T) {
	l := Limits{DocumentBytes: 10}.withDefaults()
	d := DefaultLimits()
	if l.DocumentBytes != 10 || l.DocumentsPerPublish != d.DocumentsPerPublish || l.RetainDone != 30*24*time.Hour || l.RetainOpen != 90*24*time.Hour {
		t.Errorf("withDefaults = %+v", l)
	}
	if d.DocumentBytes != 512<<10 || d.StoredBytes != 1<<30 || d.OpenReviews != 200 || d.PublishesPerHour != 30 || d.RevisionsPerReview != 50 || d.DocumentsPerPublish != 20 {
		t.Errorf("defaults = %+v", d)
	}
}
