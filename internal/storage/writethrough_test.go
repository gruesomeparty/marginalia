package storage_test

import (
	"context"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
)

func TestWriteThroughConforms(t *testing.T) {
	storagetest.Run(t, func(*testing.T) storage.Store { return storage.NewWriteThrough(storage.NewMemory()) })
	storagetest.Run(t, func(t *testing.T) storage.Store { return storage.NewWriteThrough(storage.NewDisk(t.TempDir())) })
}

// After the first read, a log costs the backend nothing to read or poll, and
// exactly one write to append — the whole point of the layer on a store that
// bills per call.
func TestWriteThroughReadsOnceAndWritesEveryLine(t *testing.T) {
	ctx := context.Background()
	backend := storagetest.NewCounting(storage.NewMemory())
	if err := backend.AppendLog(ctx, "reviews/a/log/spec.md", []byte(`{"n":0}`)); err != nil {
		t.Fatal(err)
	}
	w := storage.NewWriteThrough(backend)
	backend.Reset()

	lines, err := w.ReadLog(ctx, "reviews/a/log/spec.md")
	if err != nil || len(lines) != 1 {
		t.Fatalf("first read: %v, %v", lines, err)
	}
	for i := 0; i < 5; i++ {
		if _, err := w.ReadLog(ctx, "reviews/a/log/spec.md"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.LogToken(ctx, "reviews/a/log/spec.md"); err != nil {
			t.Fatal(err)
		}
	}
	if got := backend.Reads(); got != 1 {
		t.Errorf("backend reads = %d, want 1: the log was read through more than once", got)
	}
	before, _ := w.LogToken(ctx, "reviews/a/log/spec.md")
	if err := w.AppendLog(ctx, "reviews/a/log/spec.md", []byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	after, _ := w.LogToken(ctx, "reviews/a/log/spec.md")
	if before == after {
		t.Error("the token did not move on append")
	}
	if got := backend.Writes(); got != 1 {
		t.Errorf("backend writes = %d, want 1: an append is one write", got)
	}
	if got := backend.Reads(); got != 1 {
		t.Errorf("backend reads = %d after the append, want still 1", got)
	}
	// And the line reached the backend before memory: a fresh reader of the
	// backend sees it.
	raw, _ := backend.ReadLog(ctx, "reviews/a/log/spec.md")
	if len(raw) != 2 || string(raw[1]) != `{"n":1}` {
		t.Errorf("the backend did not get the line: %q", raw)
	}
}
