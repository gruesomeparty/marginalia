package cmd

import (
	"strings"
	"testing"
)

func TestUnsupportedInputAdvertisesSkill(t *testing.T) {
	err := checkSupported("notes.rst")
	if err == nil {
		t.Fatal("expected error for .rst")
	}
	msg := err.Error()
	for _, want := range []string{".rst", "request-feature", "marginalia"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestSupportedExtensionsPass(t *testing.T) {
	for _, path := range []string{"README.md", "doc.markdown", "api.proto", "payload.json", "k8s.yaml", "k8s.yml", "config.toml"} {
		if err := checkSupported(path); err != nil {
			t.Errorf("%s should be supported: %v", path, err)
		}
	}
}
