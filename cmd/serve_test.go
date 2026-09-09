package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildServerValidDoc(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(doc, []byte("# Hi\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := buildServer(doc, "127.0.0.1", 0, false, "tester")
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv == nil {
		t.Fatal("nil server for valid doc")
	}
}

func TestBuildServerUnsupportedExtension(t *testing.T) {
	_, err := buildServer("notes.rst", "127.0.0.1", 0, false, "tester")
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise-on-error, got %v", err)
	}
}

func TestBuildServerMissingFile(t *testing.T) {
	_, err := buildServer(filepath.Join(t.TempDir(), "nope.md"), "127.0.0.1", 0, false, "tester")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDefaultAuthor(t *testing.T) {
	t.Setenv("USER", "alice")
	if got := defaultAuthor(); got != "alice" {
		t.Errorf("defaultAuthor = %q, want alice", got)
	}
	t.Setenv("USER", "")
	if got := defaultAuthor(); got != "reviewer" {
		t.Errorf("defaultAuthor fallback = %q, want reviewer", got)
	}
}

func TestServeCommandUnsupportedExtension(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"serve", "notes.rst"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise error, got %v", err)
	}
}
