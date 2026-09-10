package cmd

import (
	"errors"
	"fmt"

	"github.com/gruesomeparty/marginalia/internal/reviewset"
)

// routeSetError sends the two failures that are really feature requests — a
// format Marginalia cannot render, and a directory holding nothing it can —
// to the request-feature skill, and passes everything else through as is.
func routeSetError(err error) error {
	var unsupported *reviewset.UnsupportedError
	if errors.As(err, &unsupported) {
		return unsupportedInputError(unsupported.Ext)
	}
	if errors.Is(err, reviewset.ErrNoDocuments) {
		return advertise(err)
	}
	return err
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
