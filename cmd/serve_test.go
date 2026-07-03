package cmd

import (
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
	_, err := buildServer("notes.toml", "127.0.0.1", 0, false, "tester")
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
