package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/review"
)

// newReviewCmd exposes the shipped framings. An agent about to impose a
// vocabulary on a human should be able to read it first — and copy it to disk
// as a starting point rather than inventing one.
func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Inspect the review framings that ship with marginalia",
	}
	cmd.AddCommand(newReviewListCmd(), newReviewShowCmd())
	return cmd
}

func newReviewListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the shipped review framings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var b strings.Builder
			for _, name := range review.PresetNames() {
				cfg, err := review.Preset(name)
				if err != nil {
					return err
				}
				fmt.Fprintf(&b, "%-10s %s\n", name, cfg.Title)
				fmt.Fprintf(&b, "%-10s %s\n", "", strings.Join(actionWords(cfg), ", "))
			}
			b.WriteString("\nserve <doc> --review <name>, or `marginalia review show <name>` to copy it to disk\n")
			_, err := io.WriteString(cmd.OutOrStdout(), b.String())
			return err
		},
	}
}

func newReviewShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print a shipped review framing as YAML",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := review.PresetSource(args[0])
			if err != nil {
				return advertise(err)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), src)
			return err
		},
	}
}

// actionWords is the vocabulary a framing imposes, in the order the reviewer
// meets it — the words that will appear as `type` in the feedback log.
func actionWords(c *review.Config) []string {
	acts := c.Actions()
	out := make([]string, 0, len(acts))
	for _, a := range acts {
		out = append(out, a.Type)
	}
	return out
}
