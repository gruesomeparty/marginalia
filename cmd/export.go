package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/diagram"
	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/reviewset"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// exportOptions is what `export` was asked for beyond the document.
type exportOptions struct {
	out      string
	config   string
	theme    string
	author   string
	diagrams string // "auto" or "off", as for serve
	mmdc     string
}

// buildExport renders the share-mode page for one document: the document as
// it now reads, the feedback already on disk, and a page that saves into the
// reviewer's own browser because there is no server on the other end.
//
// This is the only place in Marginalia where a human has to copy something
// out, and it exists for exactly one reason: sharing a review with someone
// who cannot reach your machine. Server mode remains the product.
func buildExport(paths []string, opts exportOptions) (path string, page []byte, err error) {
	set, err := reviewset.Load(paths)
	if err != nil {
		return "", nil, routeSetError(err)
	}
	if len(set.Docs) != 1 {
		return "", nil, advertise(fmt.Errorf("export takes one document at a time; %d were resolved from %v", len(set.Docs), paths))
	}
	src := set.Docs[0].Path
	doc, err := document.Parse(src)
	if err != nil {
		return "", nil, err
	}
	cfg := review.Default()
	if opts.config != "" {
		if cfg, err = review.Load(opts.config); err != nil {
			return "", nil, err
		}
	}
	theme, err := web.ThemeFor(opts.theme)
	if err != nil {
		return "", nil, advertise(err)
	}
	// A shared page carries its pictures as SVG: that is the one rendering
	// route that needs no script on the other side, which is the whole point
	// of share mode.
	drawer, err := diagrams(opts.diagrams, opts.mmdc)
	if err != nil {
		return "", nil, err
	}
	if _, failures := diagram.Attach(doc, drawer); len(failures) > 0 {
		return "", nil, fmt.Errorf("a diagram could not be drawn: %w (use --diagrams=off to share the source instead)", failures[0])
	}
	events, err := feedback.NewStore(src).Load()
	if err != nil {
		return "", nil, err
	}
	author := opts.author
	if author == "" {
		author = defaultAuthor()
	}
	var buf bytes.Buffer
	err = web.Render(&buf, web.Page{
		Doc:        doc,
		Events:     events,
		Resolution: resolutionOf(doc, events),
		Author:     author,
		Review:     cfg,
		Theme:      theme,
		Static:     true,
	})
	if err != nil {
		return "", nil, err
	}
	out := opts.out
	if out == "" {
		out = src + ".review.html"
	}
	return out, buf.Bytes(), nil
}

// resolutionOf materializes a log against the document as it now reads, so a
// shared page carries the same second-pass view the server would show.
func resolutionOf(doc *document.Document, events []feedback.Event) feedback.Resolution {
	hashes := make(map[string]string, len(doc.Blocks))
	order := make([]string, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		hashes[b.ID] = b.Hash
		order = append(order, b.ID)
	}
	return feedback.Materialize(events, hashes, order)
}

func newExportCmd() *cobra.Command {
	var opts exportOptions
	cmd := &cobra.Command{
		Use:   "export <doc>",
		Short: "Write a self-contained review page for someone off your machine",
		Long: "Renders one document as a single HTML file that needs no server: the\n" +
			"reviewer's comments live in their browser until they export them, and\n" +
			"`marginalia import` merges them back. Use `serve` unless the reviewer\n" +
			"cannot reach your machine — server mode needs no export step at all.",
		Args: requirePaths("export"),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, page, err := buildExport(args, opts)
			if err != nil {
				return err
			}
			if dir := filepath.Dir(out); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			if err := os.WriteFile(out, page, 0o644); err != nil {
				return err
			}
			// One checked write: a report built in memory cannot fail
			// halfway through and leave the caller guessing.
			_, err = io.WriteString(cmd.OutOrStdout(), fmt.Sprintf(
				"marginalia: wrote %s (%d KB)\nmarginalia: send it on; merge their reply with `marginalia import <their-file>.json`\n",
				out, len(page)/1024))
			return err
		},
	}
	cmd.Flags().StringVarP(&opts.out, "out", "o", "", "output file (default <doc>.review.html)")
	cmd.Flags().StringVar(&opts.config, "config", "", "review config: instructions, custom actions, read-only blocks (YAML)")
	cmd.Flags().StringVar(&opts.theme, "theme", "", "page palette: "+themeList())
	cmd.Flags().StringVar(&opts.author, "author", "", "review author (defaults to $USER)")
	cmd.Flags().StringVar(&opts.diagrams, "diagrams", "auto", "draw mermaid diagrams into the file: auto (when mermaid-cli is installed), off")
	cmd.Flags().StringVar(&opts.mmdc, "mmdc", "", "path to mermaid-cli (default: mmdc on PATH)")
	return cmd
}
