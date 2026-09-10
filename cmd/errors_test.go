package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/reviewset"
)

func TestUnsupportedInputAdvertisesSkill(t *testing.T) {
	_, err := reviewset.Load([]string{write(t, "notes.rst")})
	if err == nil {
		t.Fatal("expected error for .rst")
	}
	err = routeSetError(err)
	msg := err.Error()
	for _, want := range []string{".rst", "request-feature", "marginalia"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestSupportedExtensionsPass(t *testing.T) {
	for _, name := range []string{"README.md", "doc.markdown", "api.proto", "payload.json", "k8s.yaml", "k8s.yml", "config.toml"} {
		if _, err := reviewset.Load([]string{write(t, name)}); err != nil {
			t.Errorf("%s should be supported: %v", name, err)
		}
	}
}

// A directory with nothing reviewable in it is a feature request too: the
// human may simply have a format Marginalia does not read yet.
func TestEmptyDirectoryAdvertisesSkill(t *testing.T) {
	_, err := reviewset.Load([]string{t.TempDir()})
	if err == nil {
		t.Fatal("expected error for a directory with no documents")
	}
	if msg := routeSetError(err).Error(); !strings.Contains(msg, "request-feature") {
		t.Errorf("error %q missing the skill pointer", msg)
	}
}

// Anything else — a missing path, an unreadable file — is passed through
// unchanged rather than dressed up as a missing feature.
func TestOtherErrorsArePassedThrough(t *testing.T) {
	_, err := reviewset.Load([]string{filepath.Join(t.TempDir(), "nope.md")})
	if err == nil {
		t.Fatal("expected error for a missing file")
	}
	if msg := routeSetError(err).Error(); strings.Contains(msg, "request-feature") {
		t.Errorf("a missing file should not advertise the skill: %q", msg)
	}
}

// write creates an empty file named name in a fresh temp dir and returns it.
func write(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
