package hosted

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// The acceptance criterion of #214: publish, finish, republish with a noted
// block and an un-noted block edited and a block removed. Every surface
// names the same changes, and they survive a restart; finishing the new
// revision clears them, also across a restart.
func TestChangesSinceTheRevisionLastFinished(t *testing.T) {
	store := storage.NewMemory()
	h, ts := open(t, store)
	v1 := "# Plan\n\nShip on Friday.\n\nKeep the cache.\n\n# Later\n\nDrop the flag.\n\nTidy up.\n"
	r := publish(t, h, "plan", Document{Name: "plan.md", Content: []byte(v1)})
	id := r.Meta.ID
	api := ts.URL + "/r/" + id + "/api/feedback"
	if code := post(t, api, feedback.Event{Doc: "plan.md", Block: "1/2", Type: feedback.TypeComment, Text: "Friday?"}); code != http.StatusCreated {
		t.Fatalf("comment: %d", code)
	}

	// An unfinished revision is no baseline: a republish now marks nothing.
	v2 := "# Plan\n\nShip on Friday!\n\nKeep the cache.\n\n# Later\n\nDrop the flag.\n\nTidy up.\n"
	if _, err := h.Republish(context.Background(), id, Publish{Docs: []Document{{Name: "plan.md", Content: []byte(v2)}}}); err != nil {
		t.Fatal(err)
	}
	if got := changed(t, h, id); !reflect.DeepEqual(got, ChangedIDs{}) {
		t.Fatalf("nothing finished yet, changes %+v", got)
	}

	if code := post(t, api, feedback.Event{Doc: "plan.md", Type: feedback.TypeReviewDone}); code != http.StatusCreated {
		t.Fatalf("done: %d", code)
	}
	v3 := "# Plan\n\nShip on Monday.\n\nKeep the cache warm.\n\n# Later\n\nDrop the flag.\n"
	if _, err := h.Republish(context.Background(), id, Publish{Docs: []Document{{Name: "plan.md", Content: []byte(v3)}}}); err != nil {
		t.Fatal(err)
	}
	want := ChangedIDs{Edited: []string{"1/2", "1/3"}, Removed: []string{"2/3"}}
	if got := changed(t, h, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes %+v, want %+v", got, want)
	}
	if row := rowOf(h, id); row.Changed != 3 {
		t.Errorf("inbox counts %d changed, want 3", row.Changed)
	}

	// A restart rebuilds the baseline from the revision it names.
	h2, ts2 := open(t, store)
	if got := changed(t, h2, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a restart, changes %+v, want %+v", got, want)
	}
	if code := post(t, ts2.URL+"/r/"+id+"/api/feedback", feedback.Event{Doc: "plan.md", Type: feedback.TypeReviewDone}); code != http.StatusCreated {
		t.Fatalf("second done: %d", code)
	}
	if got := changed(t, h2, id); !reflect.DeepEqual(got, ChangedIDs{}) {
		t.Fatalf("finished, changes %+v", got)
	}
	h3, _ := open(t, store)
	if got := changed(t, h3, id); !reflect.DeepEqual(got, ChangedIDs{}) {
		t.Errorf("finished and restarted, changes %+v", got)
	}
}

// ChangedIDs is review_status's changed_blocks with empty lists as nil, so
// a test can compare it whole.
type ChangedIDs struct{ Added, Edited, Removed []string }

func changed(t *testing.T, h *Host, id string) ChangedIDs {
	t.Helper()
	_, targets, err := h.targets(id, "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := statuses(targets)
	if err != nil {
		t.Fatal(err)
	}
	c := st[0].Changed
	nilIfEmpty := func(s []string) []string {
		if len(s) == 0 {
			return nil
		}
		return s
	}
	return ChangedIDs{nilIfEmpty(c.Added), nilIfEmpty(c.Edited), nilIfEmpty(c.Removed)}
}

func rowOf(h *Host, id string) (row struct{ Changed int }) {
	for _, r := range h.rows() {
		if r.ID == id {
			row.Changed = r.Changed
		}
	}
	return row
}
