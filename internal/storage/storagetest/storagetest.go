// Package storagetest is the conformance suite every storage backend runs.
// A backend that passes it keeps the two guarantees the rest of Marginalia
// is built on — a blob is written once, a log only ever grows — and the
// smaller contracts around them: ordering, concurrency, prefixes, tokens,
// not-found, and a move that can be interrupted and finished.
package storagetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/storage"
)

// Run drives the suite against fresh stores from open, one per subtest.
func Run(t *testing.T, open func(t *testing.T) storage.Store) {
	t.Helper()
	for name, fn := range map[string]func(*testing.T, storage.Store){
		"BlobRoundTrip":                 blobRoundTrip,
		"PutIsPutIfAbsent":              putIfAbsent,
		"GetMissingIsNotFound":          getMissing,
		"ListByPrefix":                  listByPrefix,
		"AppendKeepsOrder":              appendOrder,
		"ConcurrentAppendsAllLand":      concurrentAppend,
		"ReadMissingLogIsEmpty":         readMissing,
		"LineCannotContainNewline":      lineNewline,
		"TokenMovesOnAppendOnly":        tokenMoves,
		"MoveRelocatesWholePrefix":      move,
		"MoveIsIdempotent":              moveTwice,
		"MoveNeverChangesContents":      moveContents,
		"KeysAreRelativeAndConfined":    badKeys,
		"ListDoesNotShowWhatIsNotThere": listEmpty,
	} {
		t.Run(name, func(t *testing.T) { fn(t, open(t)) })
	}
}

var ctx = context.Background()

func blobRoundTrip(t *testing.T, s storage.Store) {
	if err := s.PutBlob(ctx, "reviews/a/rev/1/spec.md", []byte("# Spec\n")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBlob(ctx, "reviews/a/rev/1/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# Spec\n" {
		t.Fatalf("got %q", got)
	}
	// Mutating what came back must not reach the store.
	got[0] = 'X'
	again, _ := s.GetBlob(ctx, "reviews/a/rev/1/spec.md")
	if string(again) != "# Spec\n" {
		t.Fatalf("the store handed out its own buffer: %q", again)
	}
}

func putIfAbsent(t *testing.T, s storage.Store) {
	if err := s.PutBlob(ctx, "reviews/a/meta/1.json", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	err := s.PutBlob(ctx, "reviews/a/meta/1.json", []byte(`{"v":2}`))
	if !errors.Is(err, storage.ErrExists) {
		t.Fatalf("second put returned %v, want ErrExists", err)
	}
	got, err := s.GetBlob(ctx, "reviews/a/meta/1.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"v":1}` {
		t.Fatalf("the refused put changed the blob: %q", got)
	}
	// A log key is taken too: a blob cannot be dropped on top of a log.
	if err := s.AppendLog(ctx, "reviews/a/log/spec.md", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBlob(ctx, "reviews/a/log/spec.md", []byte("x")); !errors.Is(err, storage.ErrExists) {
		t.Fatalf("put over a log returned %v, want ErrExists", err)
	}
}

func getMissing(t *testing.T, s storage.Store) {
	_, err := s.GetBlob(ctx, "reviews/none/meta/1.json")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func listByPrefix(t *testing.T, s storage.Store) {
	for _, key := range []string{"reviews/b/meta/1.json", "reviews/a/meta/2.json", "reviews/a/meta/1.json", "reviews/a/rev/1/spec.md", "reviews/ab/meta/1.json"} {
		if err := s.PutBlob(ctx, key, []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendLog(ctx, "reviews/a/log/spec.md", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"reviews/a/":      {"reviews/a/log/spec.md", "reviews/a/meta/1.json", "reviews/a/meta/2.json", "reviews/a/rev/1/spec.md"},
		"reviews/a/meta/": {"reviews/a/meta/1.json", "reviews/a/meta/2.json"},
		"reviews/a":       {"reviews/a/log/spec.md", "reviews/a/meta/1.json", "reviews/a/meta/2.json", "reviews/a/rev/1/spec.md", "reviews/ab/meta/1.json"},
		"reviews/c/":      {},
		"":                {"reviews/a/log/spec.md", "reviews/a/meta/1.json", "reviews/a/meta/2.json", "reviews/a/rev/1/spec.md", "reviews/ab/meta/1.json", "reviews/b/meta/1.json"},
	}
	for prefix, want := range cases {
		got, err := s.List(ctx, prefix)
		if err != nil {
			t.Fatalf("list %q: %v", prefix, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("list %q = %v, want %v", prefix, got, want)
		}
	}
}

func listEmpty(t *testing.T, s storage.Store) {
	got, err := s.List(ctx, "reviews/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("an empty store listed %v", got)
	}
}

func appendOrder(t *testing.T, s storage.Store) {
	for i := 0; i < 20; i++ {
		if err := s.AppendLog(ctx, "reviews/a/log/spec.md", []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := s.ReadLog(ctx, "reviews/a/log/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 20 {
		t.Fatalf("got %d lines, want 20", len(lines))
	}
	for i, l := range lines {
		if string(l) != fmt.Sprintf(`{"n":%d}`, i) {
			t.Fatalf("line %d = %q: appends came back out of order", i, l)
		}
	}
}

func concurrentAppend(t *testing.T, s storage.Store) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.AppendLog(ctx, "reviews/a/log/spec.md", []byte(`{"type":"comment"}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	lines, err := s.ReadLog(ctx, "reviews/a/log/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 50 {
		t.Fatalf("got %d lines, want 50: concurrent appends lost or tore a line", len(lines))
	}
	for _, l := range lines {
		if string(l) != `{"type":"comment"}` {
			t.Fatalf("a line was torn: %q", l)
		}
	}
}

func readMissing(t *testing.T, s storage.Store) {
	lines, err := s.ReadLog(ctx, "reviews/a/log/spec.md")
	if err != nil {
		t.Fatalf("a log nobody has written to is empty, not an error: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("got %v", lines)
	}
}

func lineNewline(t *testing.T, s storage.Store) {
	err := s.AppendLog(ctx, "reviews/a/log/spec.md", []byte("{}\n{}"))
	if !errors.Is(err, storage.ErrInvalidLine) {
		t.Fatalf("got %v, want ErrInvalidLine", err)
	}
	lines, _ := s.ReadLog(ctx, "reviews/a/log/spec.md")
	if len(lines) != 0 {
		t.Fatalf("the refused line was written: %q", lines)
	}
}

func tokenMoves(t *testing.T, s storage.Store) {
	const log = "reviews/a/log/spec.md"
	before, err := s.LogToken(ctx, log)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := s.LogToken(ctx, log)
	if same != before {
		t.Fatalf("the token moved with nothing appended: %q then %q", before, same)
	}
	if err := s.AppendLog(ctx, log, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	after, _ := s.LogToken(ctx, log)
	if after == before {
		t.Fatalf("the token did not move on append: %q", after)
	}
	// Reads, listing and blobs elsewhere leave it alone.
	_, _ = s.ReadLog(ctx, log)
	_, _ = s.List(ctx, "reviews/")
	_ = s.PutBlob(ctx, "reviews/a/meta/1.json", []byte("{}"))
	still, _ := s.LogToken(ctx, log)
	if still != after {
		t.Fatalf("the token moved without an append: %q then %q", after, still)
	}
	if err := s.AppendLog(ctx, log, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	next, _ := s.LogToken(ctx, log)
	if next == after {
		t.Fatalf("the second append did not move the token: %q", next)
	}
}

func seedReview(t *testing.T, s storage.Store, id string) {
	t.Helper()
	for _, key := range []string{"meta/1.json", "rev/1/spec.md", "rev/2/spec.md"} {
		if err := s.PutBlob(ctx, "reviews/"+id+"/"+key, []byte(id+":"+key)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := s.AppendLog(ctx, "reviews/"+id+"/log/spec.md", []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
}

func move(t *testing.T, s storage.Store) {
	seedReview(t, s, "a")
	seedReview(t, s, "b")
	if err := s.Move(ctx, "reviews/a/", "archive/a/"); err != nil {
		t.Fatal(err)
	}
	left, _ := s.List(ctx, "reviews/a/")
	if len(left) != 0 {
		t.Fatalf("keys left behind: %v", left)
	}
	moved, _ := s.List(ctx, "archive/a/")
	want := []string{"archive/a/log/spec.md", "archive/a/meta/1.json", "archive/a/rev/1/spec.md", "archive/a/rev/2/spec.md"}
	if fmt.Sprint(moved) != fmt.Sprint(want) {
		t.Fatalf("archived keys = %v, want %v", moved, want)
	}
	untouched, _ := s.List(ctx, "reviews/b/")
	if len(untouched) != 4 {
		t.Fatalf("the move touched another review: %v", untouched)
	}
	lines, err := s.ReadLog(ctx, "archive/a/log/spec.md")
	if err != nil || len(lines) != 3 || string(lines[2]) != `{"n":2}` {
		t.Fatalf("the moved log reads %v, %v", lines, err)
	}
	// The log stays append-only where it landed.
	if err := s.AppendLog(ctx, "archive/a/log/spec.md", []byte(`{"n":3}`)); err != nil {
		t.Fatal(err)
	}
	lines, _ = s.ReadLog(ctx, "archive/a/log/spec.md")
	if len(lines) != 4 {
		t.Fatalf("append after move: %d lines", len(lines))
	}
}

func moveTwice(t *testing.T, s storage.Store) {
	seedReview(t, s, "a")
	if err := s.Move(ctx, "reviews/a/", "archive/a/"); err != nil {
		t.Fatal(err)
	}
	// Nothing left to move is not an error: a sweep that was interrupted
	// runs again and must be able to finish what it started.
	if err := s.Move(ctx, "reviews/a/", "archive/a/"); err != nil {
		t.Fatalf("second move: %v", err)
	}
	moved, _ := s.List(ctx, "archive/a/")
	if len(moved) != 4 {
		t.Fatalf("archived keys after a repeat = %v", moved)
	}
}

func moveContents(t *testing.T, s storage.Store) {
	seedReview(t, s, "a")
	if err := s.Move(ctx, "reviews/a/", "archive/a/"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"meta/1.json", "rev/1/spec.md", "rev/2/spec.md"} {
		got, err := s.GetBlob(ctx, "archive/a/"+key)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "a:"+key {
			t.Fatalf("archive/a/%s = %q", key, got)
		}
		if _, err := s.GetBlob(ctx, "reviews/a/"+key); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("reviews/a/%s still readable after move: %v", key, err)
		}
	}
	// And it is still a blob: put-if-absent holds at the new key.
	if err := s.PutBlob(ctx, "archive/a/meta/1.json", []byte("x")); !errors.Is(err, storage.ErrExists) {
		t.Fatalf("put over a moved blob returned %v", err)
	}
}

func badKeys(t *testing.T, s storage.Store) {
	for _, key := range []string{"", "/abs", "a//b", "a/../b", "../x", ".", "..", "a/./b", "trailing/", "back\\slash"} {
		if err := s.PutBlob(ctx, key, []byte("x")); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("PutBlob(%q) = %v, want ErrInvalidKey", key, err)
		}
		if err := s.AppendLog(ctx, key, []byte("x")); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("AppendLog(%q) = %v, want ErrInvalidKey", key, err)
		}
		if _, err := s.GetBlob(ctx, key); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("GetBlob(%q) = %v, want ErrInvalidKey", key, err)
		}
	}
	if err := s.Move(ctx, "", "archive/"); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("Move everything = %v, want ErrInvalidKey", err)
	}
	if err := s.Move(ctx, "../", "archive/"); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("Move from .. = %v, want ErrInvalidKey", err)
	}
	// Nothing above left a trace.
	keys, _ := s.List(ctx, "")
	if len(keys) != 0 {
		t.Errorf("refused keys were written: %v", keys)
	}
}
