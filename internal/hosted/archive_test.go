package hosted

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// form posts an inbox button the way a browser on the same origin does.
func form(t *testing.T, target, back, site string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", target, strings.NewReader(url.Values{"back": {back}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", site)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode, res.Header.Get("Location")
}

// The reviewer archives from the inbox and restores from the archive tab;
// the review comes back whole, across a restart, and the restore counts as
// activity so the sweep leaves it alone.
func TestArchiveAndRestoreFromTheInbox(t *testing.T) {
	for name, store := range backends(t) {
		t.Run(name, func(t *testing.T) {
			clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
			h, ts := openLimited(t, store, Limits{}, &clock)
			r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
			id := r.Meta.ID
			if code := post(t, ts.URL+"/r/"+id+"/api/feedback", feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "keep me"}); code != http.StatusCreated {
				t.Fatal(code)
			}

			if code, loc := form(t, ts.URL+"/api/reviews/"+id+"/archive", "/?tab=done", "same-origin"); code != http.StatusSeeOther || loc != "/?tab=done" {
				t.Fatalf("archive: %d → %q", code, loc)
			}
			if code, _ := get(t, ts.URL+"/r/"+id+"/"); code != http.StatusNotFound {
				t.Errorf("an archived review is still served: %d", code)
			}
			// Back and away again in the same process: the write-through
			// cache and the byte meter both follow the moves.
			before := h.use.stored
			if _, err := h.Restore(context.Background(), id, "test"); err != nil {
				t.Fatal(err)
			}
			if code, page := get(t, ts.URL+"/r/"+id+"/api/feedback?doc=spec.md"); code != 200 || !strings.Contains(page, "keep me") {
				t.Fatalf("restored in-process without its notes: %d %s", code, page)
			}
			if h.use.stored <= before {
				t.Errorf("a restore did not count the review's bytes again: %d → %d", before, h.use.stored)
			}
			if err := h.Archive(context.Background(), id, "test"); err != nil {
				t.Fatal(err)
			}
			if h.use.stored != before {
				t.Errorf("archiving again left %d bytes counted, want %d", h.use.stored, before)
			}
			if _, body := get(t, ts.URL+"/"); strings.Contains(body, id) {
				t.Error("an archived review is still in the default tab")
			}

			// A fresh process on the same store lists it in the archive.
			clock = clock.Add(200 * 24 * time.Hour)
			h2, ts2 := openLimited(t, store, Limits{}, &clock)
			_, body := get(t, ts2.URL+"/?tab=archived")
			if !strings.Contains(body, `data-review="`+id+`"`) || !strings.Contains(body, "/api/reviews/"+id+"/restore") {
				t.Fatalf("the archive tab does not offer the review back:\n%s", body)
			}
			if code, _ := form(t, ts2.URL+"/api/reviews/"+id+"/restore", "", "same-origin"); code != http.StatusSeeOther {
				t.Fatalf("restore: %d", code)
			}
			if code, page := get(t, ts2.URL+"/r/"+id+"/api/feedback?doc=spec.md"); code != 200 || !strings.Contains(page, "keep me") {
				t.Fatalf("the restored review lost its notes: %d %s", code, page)
			}
			// Last published 200 days ago, restored just now: not swept.
			if archived, err := h2.Sweep(context.Background()); err != nil || len(archived) != 0 {
				t.Errorf("the sweep re-archived a review restored a moment ago: %v %v", archived, err)
			}
			// And the restore survives a restart, from its marker alone.
			h3, _ := openLimited(t, store, Limits{}, &clock)
			if got := h3.Review(id); got == nil || !got.Restored.Equal(clock) {
				t.Errorf("after a restart, restored = %v, want %v", got, clock)
			}
			if archived, _ := h3.Sweep(context.Background()); len(archived) != 0 {
				t.Errorf("a restart forgot the restore: swept %v", archived)
			}
		})
	}
}

// A forged cross-site form is refused, and a form cannot send the reviewer
// anywhere but back to this host.
func TestArchiveRefusesCrossSiteAndForeignRedirects(t *testing.T) {
	h, ts := open(t, storage.NewMemory())
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	if code, _ := form(t, ts.URL+"/api/reviews/"+r.Meta.ID+"/archive", "/", "cross-site"); code != http.StatusForbidden {
		t.Errorf("cross-site archive: %d, want 403", code)
	}
	if h.Review(r.Meta.ID) == nil {
		t.Fatal("a cross-site post archived the review")
	}
	for _, back := range []string{"//evil.example/", "https://evil.example/", "/\\evil.example", "/\t/evil.example", "/\n/evil.example", "inbox"} {
		if got := localBack(back); got != "/" {
			t.Errorf("localBack(%q) = %q, want /", back, got)
		}
	}
	if code, _ := form(t, ts.URL+"/api/reviews/AAAAAAAAAAAAAAAAAAAAAA/archive", "/", "same-origin"); code != http.StatusNotFound {
		t.Errorf("archiving an unknown review: %d, want 404", code)
	}
}

// An agent withdraws only what it published, and only before the reviewer
// has written anything on it.
func TestWithdrawIsThePublishersUntilTheReviewerStarts(t *testing.T) {
	h, ts := open(t, storage.NewMemory())
	ctx := context.Background()
	mine := publish(t, h, "mine", Document{Name: "spec.md", Content: []byte(specDoc)})
	theirs, err := h.Publish(ctx, Publish{Title: "theirs", Agent: "other", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	started := publish(t, h, "started", Document{Name: "spec.md", Content: []byte(specDoc)})
	if code := post(t, ts.URL+"/r/"+started.Meta.ID+"/api/feedback", feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "n"}); code != http.StatusCreated {
		t.Fatal(code)
	}

	cs := connect(t, ts)
	var out withdrawOut
	if err := call(t, cs, "withdraw_review", map[string]any{"review_id": theirs.Meta.ID}, &out); err == nil || !strings.Contains(err.Error(), "another agent") {
		t.Errorf("withdrawing another agent's review: %v", err)
	}
	if err := call(t, cs, "withdraw_review", map[string]any{"review_id": started.Meta.ID}, &out); err == nil || !strings.Contains(err.Error(), "started on it") {
		t.Errorf("withdrawing a review the reviewer started: %v", err)
	}
	if err := call(t, cs, "withdraw_review", map[string]any{"review_id": mine.Meta.ID}, &out); err != nil || !out.Archived {
		t.Fatalf("withdrawing its own untouched review: %v %+v", err, out)
	}
	if h.Review(mine.Meta.ID) != nil || h.Review(theirs.Meta.ID) == nil || h.Review(started.Meta.ID) == nil {
		t.Error("withdraw moved the wrong reviews")
	}
	if got := h.ArchivedReviews(); len(got) != 1 || got[0].Meta.ID != mine.Meta.ID {
		t.Errorf("archive holds %v", got)
	}
}

// A note routed to a review's server just as it is archived must not land
// beside the archive, where a restore would have to pick one log over the
// other: once archiving starts, the old server's appends are refused.
func TestArchiveFreezesTheOldServersAppends(t *testing.T) {
	store := storage.NewMemory()
	h, _ := open(t, store)
	ctx := context.Background()
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	stale := r.Server.Entries()[0].Store
	if err := stale.Append(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "before", Ts: "2026-10-01T09:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Archive(ctx, r.Meta.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if err := stale.Append(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "late", Ts: "2026-10-01T09:00:01Z"}); err == nil {
		t.Fatal("an append to an archived review's old server was accepted")
	}
	if keys, _ := store.List(ctx, prefixReviews); len(keys) != 0 {
		t.Fatalf("something landed beside the archive: %v", keys)
	}
	back, err := h.Restore(ctx, r.Meta.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	events, err := back.Server.Entries()[0].Store.Load()
	if err != nil || len(events) != 1 || events[0].Text != "before" {
		t.Fatalf("restored log = %v, %v", events, err)
	}
	if err := back.Server.Entries()[0].Store.Append(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "after", Ts: "2026-10-01T09:00:02Z"}); err != nil {
		t.Errorf("a restored review refuses notes: %v", err)
	}
}
