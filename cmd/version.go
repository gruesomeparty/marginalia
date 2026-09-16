package cmd

import (
	"github.com/gruesomeparty/marginalia/internal/session"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// The release ldflags write to cmd.version, so session takes its copy from
// here rather than the other way round.
func init() { session.Version = version }

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("marginalia %s (commit %s, built %s)\n", version, commit, date)
			return nil
		},
	}
}
