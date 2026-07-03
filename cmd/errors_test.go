package cmd

import (
	"strings"
	"testing"
)

func TestUnsupportedInputAdvertisesSkill(t *testing.T) {
	err := checkSupported("notes.toml")
	if err == nil {
		t.Fatal("expected error for .toml")
	}
	msg := err.Error()
	for _, want := range []string{".toml", "request-feature", "marginalia"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestSupportedExtensionsPass(t *testing.T) {
	if err := checkSupported("README.md"); err != nil {
		t.Fatalf("md should be supported: %v", err)
	}
	if err := checkSupported("doc.markdown"); err != nil {
		t.Fatalf("markdown should be supported: %v", err)
	}
}
