package cmd

import (
	"io"
	"net/http/httptest"
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
	srv, err := buildServer([]string{doc}, serveOptions{host: "127.0.0.1", author: "tester"})
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv == nil {
		t.Fatal("nil server for valid doc")
	}
}

func TestBuildServerUnsupportedExtension(t *testing.T) {
	_, err := buildServer([]string{write(t, "notes.rst")}, serveOptions{host: "127.0.0.1", author: "tester"})
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise-on-error, got %v", err)
	}
}

// A path that isn't there is a typo, not a missing feature — it must not be
// dressed up as one, or the feedback loop fills with requests for files that
// never existed.
func TestBuildServerMissingFileIsNotAFeatureRequest(t *testing.T) {
	_, err := buildServer([]string{filepath.Join(t.TempDir(), "gone.rst")}, serveOptions{host: "127.0.0.1", author: "tester"})
	if err == nil || strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want a plain not-found error, got %v", err)
	}
}

func TestBuildServerMissingFile(t *testing.T) {
	_, err := buildServer([]string{filepath.Join(t.TempDir(), "nope.md")}, serveOptions{host: "127.0.0.1", author: "tester"})
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

// A review config reaches the server through `serve --config`, and a config
// that cannot mean what it says fails at startup rather than silently
// serving the default review.
func TestBuildServerWithReviewConfig(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "d.md")
	if err := os.WriteFile(doc, []byte("# Hi\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "review.yaml")
	if err := os.WriteFile(cfg, []byte("instructions: Look at the cap.\nactions:\n  - type: blocker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := buildServer([]string{doc}, serveOptions{host: "127.0.0.1", author: "tester", config: cfg})
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rr.Body.String(), "Look at the cap.") {
		t.Error("the served page does not carry the review's instructions")
	}
	if !strings.Contains(rr.Body.String(), `"type":"blocker"`) {
		t.Error("the served page does not offer the configured action")
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("actions:\n  - type: Blocker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildServer([]string{doc}, serveOptions{config: bad}); err == nil {
		t.Error("a config with an invalid action must fail at startup")
	}
	if _, err := buildServer([]string{doc}, serveOptions{config: filepath.Join(dir, "gone.yaml")}); err == nil {
		t.Error("a config that isn't there must fail at startup")
	}
}
