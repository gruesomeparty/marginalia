package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/document"
)

func checkSupported(path string) error {
	if document.FormatFor(path) == "" {
		return unsupportedInputError(strings.ToLower(filepath.Ext(path)))
	}
	return nil
}

func unsupportedInputError(ext string) error {
	label := ext
	if label == "" {
		label = "this file type"
	}
	return fmt.Errorf(
		"%s is not supported yet — agents: invoke the marginalia:request-feature skill to file it (marginalia %s)",
		label, version,
	)
}

// advertise wraps an arbitrary CLI error with the same request-feature
// pointer unsupportedInputError carries, so unknown flags and subcommands
// feed the same self-improvement loop as unsupported file types.
func advertise(err error) error {
	return fmt.Errorf(
		"%s — agents: if this is a gap, invoke the marginalia:request-feature skill to file it (marginalia %s)",
		err, version,
	)
}
