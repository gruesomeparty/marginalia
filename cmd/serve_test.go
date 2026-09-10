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
	srv, err := buildServer([]string{doc}, serveOptions{Host: "127.0.0.1", Author: "tester"})
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv == nil {
		t.Fatal("nil server for valid doc")
	}
}

func TestBuildServerUnsupportedExtension(t *testing.T) {
	_, err := buildServer([]string{write(t, "notes.rst")}, serveOptions{Host: "127.0.0.1", Author: "tester"})
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise-on-error, got %v", err)
	}
}

// A path that isn't there is a typo, not a missing feature — it must not be
// dressed up as one, or the feedback loop fills with requests for files that
// never existed.
func TestBuildServerMissingFileIsNotAFeatureRequest(t *testing.T) {
	_, err := buildServer([]string{filepath.Join(t.TempDir(), "gone.rst")}, serveOptions{Host: "127.0.0.1", Author: "tester"})
	if err == nil || strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want a plain not-found error, got %v", err)
	}
}

func TestBuildServerMissingFile(t *testing.T) {
	_, err := buildServer([]string{filepath.Join(t.TempDir(), "nope.md")}, serveOptions{Host: "127.0.0.1", Author: "tester"})
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
	root.SetArgs([]string{"serve", write(t, "notes.rst")})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise error, got %v", err)
	}
}

// Forgetting the path is a usage mistake, not a missing feature: it must say
// what to type and must not route the human to the feature tracker.
func TestServeWithoutArgsExplainsItself(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"serve"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error with no arguments")
	}
	msg := err.Error()
	if !strings.Contains(msg, "document or directory") || !strings.Contains(msg, "marginalia serve") {
		t.Errorf("error %q should say what to type", msg)
	}
	if strings.Contains(msg, "request-feature") {
		t.Errorf("a usage mistake should not advertise the feature tracker: %q", msg)
	}
}
