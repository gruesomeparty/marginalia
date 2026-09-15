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
	srv, err := buildServer([]string{doc}, serveOptions{Host: "127.0.0.1", Author: "tester", Config: cfg})
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
	if _, err := buildServer([]string{doc}, serveOptions{Config: bad}); err == nil {
		t.Error("a config with an invalid action must fail at startup")
	}
	if _, err := buildServer([]string{doc}, serveOptions{Config: filepath.Join(dir, "gone.yaml")}); err == nil {
		t.Error("a config that isn't there must fail at startup")
	}
}

// A theme nobody has written yet is a capability gap, so it goes to the
// feedback loop rather than dying as a typo.
func TestBuildServerUnknownTheme(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(doc, []byte("# Hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := buildServer([]string{doc}, serveOptions{Theme: "catppuccin-frappe"})
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise-on-error, got %v", err)
	}
	srv, err := buildServer([]string{doc}, serveOptions{Theme: "catppuccin-mocha"})
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rr.Body.String(), `data-palette="catppuccin"`) {
		t.Error("the served page is not painted in the chosen theme")
	}
}

// A shipped framing is selected by name, and an unknown one names the ones
// that exist rather than silently serving the default review.
func TestReviewConfigResolvesPresetsAndOverlays(t *testing.T) {
	cfg, err := reviewConfig("security", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Title != "Security review" {
		t.Errorf("preset not loaded: %q", cfg.Title)
	}
	var found bool
	for _, a := range cfg.Actions() {
		if a.Type == "vulnerability" {
			found = true
		}
	}
	if !found {
		t.Errorf("the security vocabulary is missing: %+v", cfg.Actions())
	}

	// No preset and no file is the plain default review, unchanged.
	plain, err := reviewConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Title != "" || len(plain.Custom) != 0 {
		t.Errorf("the default review grew a framing: %+v", plain)
	}

	// A file layers over a preset.
	path := filepath.Join(t.TempDir(), "over.yaml")
	if err := os.WriteFile(path, []byte("title: Ingest security review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layered, err := reviewConfig("security", path)
	if err != nil {
		t.Fatal(err)
	}
	if layered.Title != "Ingest security review" {
		t.Errorf("the file did not win on title: %q", layered.Title)
	}
	if len(layered.Actions()) != len(cfg.Actions()) {
		t.Error("layering a title dropped the preset's vocabulary")
	}

	_, err = reviewConfig("architecture", "")
	if err == nil {
		t.Fatal("an unknown framing was accepted")
	}
	// The error advertises the feedback loop, like every other unknown option.
	for _, want := range []string{"adr", "request-feature"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("unhelpful error, missing %q: %v", want, err)
		}
	}
}

func TestPresetListNamesTheShippedFramings(t *testing.T) {
	for _, want := range []string{"adr", "copy", "schema", "security"} {
		if !strings.Contains(presetList(), want) {
			t.Errorf("flag help does not offer %q: %s", want, presetList())
		}
	}
}
