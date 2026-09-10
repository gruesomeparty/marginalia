package feedback

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestNewStorePath(t *testing.T) {
	s := NewStore("specs/foo.md")
	if s.Path() != "specs/foo.md.feedback.jsonl" {
		t.Fatalf("path = %s", s.Path())
	}
}

func TestAppendLoadRoundTrip(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "d.md")
	s := NewStore(doc)
	in := Event{Doc: doc, Block: "1/2", Type: TypeComment, Text: "hi", Author: "b", Ts: "2026-07-03T10:00:00Z"}
	if err := s.Append(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "hi" || got[0].Block != "1/2" {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "none.md"))
	got, err := s.Load()
	if err != nil || got != nil {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestConcurrentAppend(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "c.md"))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Append(Event{Block: "1/1", Type: TypeComment, Text: "x"})
		}()
	}
	wg.Wait()
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("got %d events, want 50", len(got))
	}
}

func TestResolveLatestPerBlock(t *testing.T) {
	events := []Event{
		{Block: "1/1", Type: TypeComment, Text: "old", Ts: "2026-07-03T10:00:00Z"},
		{Block: "1/1", Type: TypeComment, Text: "new", Ts: "2026-07-03T11:00:00Z"},
		{Block: "2/1", Type: TypeApprove, Ts: "2026-07-03T10:30:00Z"},
		{Type: TypeReviewDone, Ts: "2026-07-03T12:00:00Z"},
	}
	got := Resolve(events)
	if got["1/1"].Text != "new" {
		t.Errorf("1/1 = %q, want new", got["1/1"].Text)
	}
	if _, ok := got[""]; ok {
		t.Error("review_done (empty block) must not appear in resolution")
	}
}
