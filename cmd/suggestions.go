package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/reviewset"
)

// applicable pairs a suggestion with the block's text as the document now
// reads, so whoever applies it can see exactly what is being swapped.
type applicable struct {
	feedback.Suggestion
	Current string `json:"current"`
}

// report is one document's suggestions, split by whether they can be applied
// without asking anyone.
type report struct {
	Doc               string                `json:"doc"`
	Applicable        []applicable          `json:"applicable"`
	NeedsConfirmation []feedback.Suggestion `json:"needs_confirmation"`
}

func newSuggestionsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "suggestions <doc|dir>...",
		Short: "List the suggest_edit replacements that are safe to apply verbatim",
		Long: `List suggest_edit feedback, split into replacements that can be applied
verbatim and ones that need a human to confirm first.

A suggestion is applicable only when it is the note that stands for its block,
its hash matches the block as the document now reads, and it carries a hash at
all. Marginalia never edits the document — this reports; the caller applies.`,
		Args: requirePaths("suggestions"),
		RunE: func(cmd *cobra.Command, args []string) error {
			reports, err := collectSuggestions(args)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(reports)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), formatReports(reports))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	return cmd
}

// collectSuggestions materializes each document's log against the document as
// it currently reads, and reports the suggestions either way.
func collectSuggestions(paths []string) ([]report, error) {
	set, err := reviewset.Load(paths)
	if err != nil {
		return nil, routeSetError(err)
	}
	reports := make([]report, 0, len(set.Docs))
	for _, d := range set.Docs {
		doc, err := document.Parse(d.Path)
		if err != nil {
			return nil, err
		}
		events, err := feedback.NewStore(d.Path).Load()
		if err != nil {
			return nil, err
		}
		hashes := make(map[string]string, len(doc.Blocks))
		text := make(map[string]string, len(doc.Blocks))
		order := make([]string, 0, len(doc.Blocks))
		for _, b := range doc.Blocks {
			hashes[b.ID] = b.Hash
			text[b.ID] = b.PlainText
			order = append(order, b.ID)
		}
		ready, needs := feedback.Materialize(events, hashes, order).Suggestions()
		r := report{Doc: d.Path, Applicable: make([]applicable, 0, len(ready)), NeedsConfirmation: needs}
		for _, s := range ready {
			r.Applicable = append(r.Applicable, applicable{Suggestion: s, Current: text[s.Block]})
		}
		if r.NeedsConfirmation == nil {
			r.NeedsConfirmation = []feedback.Suggestion{}
		}
		reports = append(reports, r)
	}
	return reports, nil
}

// formatReports renders the human-readable report. Built in memory so the one
// write to the command's output is the only thing that can fail — and does not
// fail silently.
func formatReports(reports []report) string {
	var b strings.Builder
	for _, r := range reports {
		if len(r.Applicable) == 0 && len(r.NeedsConfirmation) == 0 {
			fmt.Fprintf(&b, "%s: no suggestions\n", r.Doc)
			continue
		}
		fmt.Fprintf(&b, "%s: %d applicable, %d need confirmation\n", r.Doc, len(r.Applicable), len(r.NeedsConfirmation))
		if len(r.Applicable) > 0 {
			b.WriteString("  applicable\n")
			for _, s := range r.Applicable {
				fmt.Fprintf(&b, "    %s (hash %s, %s)\n", s.Block, s.Hash, s.Author)
				fmt.Fprintf(&b, "      current: %s\n", s.Current)
				fmt.Fprintf(&b, "      replace: %s\n", s.Replacement)
			}
		}
		if len(r.NeedsConfirmation) > 0 {
			b.WriteString("  need confirmation\n")
			for _, s := range r.NeedsConfirmation {
				fmt.Fprintf(&b, "    %s — %s\n", s.Block, s.Reason)
				fmt.Fprintf(&b, "      was:     %s\n", s.Quote)
				fmt.Fprintf(&b, "      replace: %s\n", s.Replacement)
			}
		}
	}
	return b.String()
}
