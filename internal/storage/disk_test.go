package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
)

func TestDiskConforms(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Store { return storage.NewDisk(t.TempDir()) })
}

func TestMemoryConforms(t *testing.T) {
	storagetest.Run(t, func(*testing.T) storage.Store { return storage.NewMemory() })
}

// The layout on disk is the layout agents and other tools read, so it is
// pinned: a key is a path under the root, a log line is the line plus "\n",
// and nothing else appears.
func TestDiskLayoutIsKeysAsPaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	d := storage.NewDisk(root)
	if err := d.AppendLog(ctx, "spec.md.feedback.jsonl", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendLog(ctx, "spec.md.feedback.jsonl", []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "spec.md.feedback.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\"a\":1}\n{\"a\":2}\n" {
		t.Fatalf("log bytes = %q", got)
	}
	if err := d.PutBlob(ctx, "reviews/x/rev/1/spec.md", []byte("# S\n")); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(root, "reviews", "x", "rev", "1", "spec.md"))
	if err != nil || string(blob) != "# S\n" {
		t.Fatalf("blob on disk = %q, %v", blob, err)
	}
	// The temp directory a put goes through is not a key and leaves nothing
	// behind in a listing.
	keys, err := d.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k == "spec.md.feedback.jsonl" || k == "reviews/x/rev/1/spec.md" {
			continue
		}
		t.Errorf("unexpected key %q", k)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, ".marginalia-tmp")); len(entries) != 0 {
		t.Errorf("temp files left behind: %d", len(entries))
	}
	if err := d.PutBlob(ctx, ".marginalia-tmp/x", []byte("x")); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("the temp directory is a key: %v", err)
	}
}

// A root that does not exist yet is created by the first write, and a read
// before that reports an empty store rather than an error.
func TestDiskCreatesItsRoot(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "data", "nested")
	d := storage.NewDisk(root)
	keys, err := d.List(ctx, "reviews/")
	if err != nil || len(keys) != 0 {
		t.Fatalf("list of an absent root = %v, %v", keys, err)
	}
	if err := d.PutBlob(ctx, "reviews/a/meta/1.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "reviews", "a", "meta", "1.json")); err != nil {
		t.Fatal(err)
	}
}

// A move leaves no empty directories behind, so a listing of the archive is
// the only trace a review was ever elsewhere.
func TestDiskMovePrunesEmptyDirectories(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	d := storage.NewDisk(root)
	if err := d.PutBlob(ctx, "reviews/a/rev/1/spec.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := d.PutBlob(ctx, "reviews/b/rev/1/spec.md", []byte("y")); err != nil {
		t.Fatal(err)
	}
	if err := d.Move(ctx, "reviews/a/", "archive/a/"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "reviews", "a")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("reviews/a still exists after the move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "reviews", "b", "rev", "1", "spec.md")); err != nil {
		t.Errorf("the other review was pruned: %v", err)
	}
}
