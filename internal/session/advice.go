// Package session builds a review server from what an agent asked for: the
// paths, the framing, the theme, the renderer.
//
// It lives under internal/ rather than in cmd/ because the CLI is no longer
// the only caller — the MCP server starts exactly the same review from a tool
// call, and a package cmd cannot be imported. What is here is the pipeline
// both use; cmd keeps the flags, mcpserver keeps the tool schemas.
package session

import (
	"errors"
	"fmt"

	"github.com/gruesomeparty/marginalia/internal/reviewset"
)

// Version is the binary's version, for the request-feature pointer. cmd sets
// it from its own `version`, which is what the release ldflags write to.
var Version = "dev"

// RouteSetError sends the two failures that are really feature requests — a
// format Marginalia cannot render, and a directory holding nothing it can —
// to the request-feature skill, and passes everything else through as is.
func RouteSetError(err error) error {
	var unsupported *reviewset.UnsupportedError
	if errors.As(err, &unsupported) {
		return UnsupportedInput(unsupported.Ext)
	}
	if errors.Is(err, reviewset.ErrNoDocuments) {
		return Advertise(err)
	}
	return err
}

// UnsupportedInput is the error for a format nobody has taught it yet.
func UnsupportedInput(ext string) error {
	label := ext
	if label == "" {
		label = "this file type"
	}
	return fmt.Errorf(
		"%s is not supported yet — agents: invoke the marginalia:request-feature skill to file it (marginalia %s)",
		label, Version,
	)
}

// Advertise wraps an arbitrary error with the same request-feature pointer
// UnsupportedInput carries, so unknown flags, unknown tool arguments and
// unsupported file types all feed the same self-improvement loop.
func Advertise(err error) error {
	return fmt.Errorf(
		"%s — agents: if this is a gap, invoke the marginalia:request-feature skill to file it (marginalia %s)",
		err, Version,
	)
}
