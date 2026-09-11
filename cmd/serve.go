package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/gruesomeparty/marginalia/internal/diagram"
	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/reviewset"
	"github.com/gruesomeparty/marginalia/internal/server"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// serveOptions is what `serve` was asked for. A struct rather than a row of
// positional booleans, which is how open and watch get swapped by accident.
type serveOptions struct {
	Host   string
	Port   int
	Open   bool
	Watch  bool
	Author string
	Config string // review config file, "" for the default review
	Theme  string // palette name, "" for the default
	// Diagrams is "auto" (draw mermaid when a renderer is installed) or
	// "off" (always show the anchored source).
	Diagrams string
	MMDC     string // explicit mermaid-cli path
}

// buildServer resolves the paths into a review set — one document, several, or
// a directory of them — parses each and wires it to its own feedback log.
func buildServer(paths []string, opts serveOptions) (*server.Server, error) {
	set, err := reviewset.Load(paths)
	if err != nil {
		return nil, routeSetError(err)
	}
	cfg := review.Default()
	if opts.Config != "" {
		if cfg, err = review.Load(opts.Config); err != nil {
			return nil, err
		}
	}
	// A theme nobody has written yet is a feature request, not a typo.
	theme, err := web.ThemeFor(opts.Theme)
	if err != nil {
		return nil, advertise(err)
	}
	drawer, err := diagrams(opts.Diagrams, opts.MMDC)
	if err != nil {
		return nil, err
	}
	docs := make([]server.Entry, 0, len(set.Docs))
	for _, d := range set.Docs {
		doc, err := document.Parse(d.Path)
		if err != nil {
			return nil, err
		}
		docs = append(docs, server.Entry{
			Doc:   doc,
			Store: feedback.NewStore(d.Path),
			Label: d.Label,
			Rel:   d.Rel,
		})
	}
	author := opts.Author
	if author == "" {
		author = defaultAuthor()
	}
	return server.New(server.Options{
		Docs:     docs,
		Title:    sessionTitle(set),
		Root:     set.Root,
		Index:    set.Index,
		Excluded: set.Excluded,
		// A discovered set mirrors the folders it was found in; a curated one
		// is exactly the index's list, so its order is the tree.
		Nested:   set.Index == "",
		Review:   cfg,
		Theme:    theme,
		Diagrams: drawer,
		Author:   author,
		Host:     opts.Host,
		Port:     opts.Port,
		Open:     opts.Open,
		Watch:    opts.Watch,
	}), nil
}

// diagrams resolves the diagram-rendering choice. "auto" draws mermaid when
// mermaid-cli is installed and shows the anchored source when it is not, so a
// machine without Node still serves every review; "off" never draws.
func diagrams(mode, bin string) (*diagram.Renderer, error) {
	switch mode {
	case "", "auto":
		return diagram.Find(bin), nil
	case "off":
		return &diagram.Renderer{}, nil
	}
	return nil, advertise(fmt.Errorf("unknown --diagrams %q — available: auto, off", mode))
}

// themeList names the palettes, for flag help.
func themeList() string { return strings.Join(web.ThemeNames(), ", ") }

// sessionTitle is what the page header calls the review: the index's title, or
// the directory the set was found in. A single document titles itself.
func sessionTitle(set *reviewset.Set) string {
	if set.Title != "" {
		return set.Title
	}
	if set.Single() {
		return ""
	}
	// A relative root ("." from inside the directory being served) is no kind
	// of name; use the directory's own.
	if abs, err := filepath.Abs(set.Root); err == nil {
		return filepath.Base(abs)
	}
	return set.Root
}

func defaultAuthor() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "reviewer"
}

// requirePaths rejects an empty argument list with an error that says what to
// type. Deliberately not routed through advertise(): the request-feature
// pointer is for capability gaps — a format we cannot render, a flag that does
// not exist — and "you forgot the path" is a usage mistake. Sending those to
// the feature tracker would fill the queue with noise, the same reason a
// missing file does not advertise either.
func requirePaths(verb string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			return nil
		}
		// Share mode's two verbs take something other than a set of
		// documents, so they say so rather than inheriting the wrong shape.
		switch verb {
		case "export":
			return errors.New("export needs a document — e.g. `marginalia export spec.md -o review.html`")
		case "import":
			return errors.New("import needs a review file — e.g. `marginalia import review.json`")
		}
		return fmt.Errorf("%s needs at least one document or directory — e.g. `marginalia %s spec.md` or `marginalia %s docs/`", verb, verb, verb)
	}
}

func newServeCmd() *cobra.Command {
	var (
		opts        serveOptions
		port        int
		host        string
		open        bool
		watch       bool
		author      string
		config      string
		theme       string
		diagramMode string
		mmdc        string
	)
	cmd := &cobra.Command{
		Use:   "serve <doc|dir>...",
		Short: "Serve one or more documents for block-anchored human review",
		Args:  requirePaths("serve"),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts = serveOptions{
				Host: host, Port: port, Open: open, Watch: watch, Author: author,
				Config: config, Theme: theme, Diagrams: diagramMode, MMDC: mmdc,
			}
			srv, err := buildServer(args, opts)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8787, "port to listen on")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host/interface to bind (set to your Tailscale IP for remote review)")
	cmd.Flags().BoolVar(&open, "open", false, "open the review page in a browser")
	cmd.Flags().BoolVar(&watch, "watch", false, "re-parse a document when its file changes; the page offers a reload")
	cmd.Flags().StringVar(&author, "author", "", "review author (defaults to $USER)")
	cmd.Flags().StringVar(&config, "config", "", "review config: instructions, custom actions, read-only blocks (YAML)")
	cmd.Flags().StringVar(&theme, "theme", "", "page palette: "+themeList())
	cmd.Flags().StringVar(&diagramMode, "diagrams", "auto", "draw mermaid diagrams: auto (when mermaid-cli is installed), off")
	cmd.Flags().StringVar(&mmdc, "mmdc", "", "path to mermaid-cli (default: mmdc on PATH)")
	return cmd
}
