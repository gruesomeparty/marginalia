package cmd

import (
	"io"
	"strings"
	"testing"
)

func TestUnknownFlagAdvertises(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"serve", "--nope"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise error for unknown flag, got %v", err)
	}
}

func TestUnknownSubcommandAdvertises(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"bogus"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := executeRoot(root)
	if err == nil || !strings.Contains(err.Error(), "request-feature") {
		t.Fatalf("want advertise error for unknown subcommand, got %v", err)
	}
}

func TestValidCommandsUnaffected(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"version"})
	root.SetOut(io.Discard)
	if err := executeRoot(root); err != nil {
		t.Fatalf("version should succeed: %v", err)
	}
}
