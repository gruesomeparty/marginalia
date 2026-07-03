package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "marginalia",
		Short:         "Hand a document to a human for block-anchored review",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Cobra falls back to the root's flag-error func for any subcommand
	// that doesn't set its own, so this also covers e.g. `serve --badflag`.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return advertise(err)
	})
	root.AddCommand(newServeCmd(), newVersionCmd())
	return root
}

// executeRoot runs root and advertises the request-feature skill on unknown
// subcommands. Unsupported-extension and flag errors already carry the
// pointer by the time they reach here, so only bare "unknown command"
// errors need wrapping.
func executeRoot(root *cobra.Command) error {
	err := root.Execute()
	if err != nil && strings.HasPrefix(err.Error(), "unknown command") {
		return advertise(err)
	}
	return err
}

// Execute runs the CLI and exits non-zero on error.
func Execute() {
	if err := executeRoot(newRootCmd()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
