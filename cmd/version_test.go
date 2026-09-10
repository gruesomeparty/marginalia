package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	// version is package-level state the release build sets via ldflags;
	// restore it so a later test cannot read this one's value.
	original := version
	t.Cleanup(func() { version = original })
	version = "1.2.3"
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "1.2.3") {
		t.Fatalf("version output %q missing version", out.String())
	}
}
