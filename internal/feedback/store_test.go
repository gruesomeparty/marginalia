package feedback

import (
	"os"
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

// The file on disk is an interface — agents tail it, `import` dedupes against
// it, other tools read it — so its bytes are pinned here exactly. A storage
// refactor that changed a byte of this would change what every reader sees.
func TestLogBytesArePinned(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "spec.md")
	s := NewStore(doc)
	events := []Event{
		{Doc: doc, Block: "1/2", Quote: "Capped at 500", Hash: "9f2c01ab54de", Type: TypeComment, Text: "why 500?", Author: "berkay", Ts: "2026-07-03T10:00:00Z"},
		{Doc: doc, Block: "1/2", Quote: "Capped at 500", Hash: "9f2c01ab54de", Type: TypeReply, Text: "batch limit", Author: "agent", Ts: "2026-07-03T10:05:00Z", ReplyTo: "abc123def456", Fields: map[string]string{"severity": "low"}},
		{Doc: doc, Block: "2/1", Type: TypeSuggestEdit, Text: "two\nlines", Author: "berkay", Ts: "2026-07-03T10:06:00Z", Sub: &Sub{Quote: "no backoff", Prefix: "attempts, ", Suffix: ".", Start: 16, Hash: "0011223344aa"}},
		{Doc: doc, Type: TypeReviewDone, Author: "berkay", Ts: "2026-07-03T10:07:00Z"},
	}
	for _, e := range events {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"doc":"` + doc + `","block":"1/2","quote":"Capped at 500","hash":"9f2c01ab54de","type":"comment","text":"why 500?","author":"berkay","ts":"2026-07-03T10:00:00Z"}` + "\n" +
		`{"doc":"` + doc + `","block":"1/2","quote":"Capped at 500","hash":"9f2c01ab54de","type":"reply","text":"batch limit","author":"agent","ts":"2026-07-03T10:05:00Z","fields":{"severity":"low"},"reply_to":"abc123def456"}` + "\n" +
		`{"doc":"` + doc + `","block":"2/1","quote":"","hash":"","type":"suggest_edit","text":"two\nlines","author":"berkay","ts":"2026-07-03T10:06:00Z","sub":{"quote":"no backoff","prefix":"attempts, ","suffix":".","start":16,"hash":"0011223344aa"}}` + "\n" +
		`{"doc":"` + doc + `","block":"","quote":"","hash":"","type":"review_done","text":"","author":"berkay","ts":"2026-07-03T10:07:00Z"}` + "\n"
	if string(got) != want {
		t.Fatalf("the log's bytes changed:\n got: %q\nwant: %q", got, want)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("log mode = %o, want 644", perm)
	}
	back, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 4 || back[2].Sub == nil || back[2].Sub.Start != 16 || back[1].Fields["severity"] != "low" {
		t.Fatalf("round trip lost something: %+v", back)
	}
}
