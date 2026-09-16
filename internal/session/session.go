package session

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/diagram"
	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
	"github.com/gruesomeparty/marginalia/internal/reviewset"
	"github.com/gruesomeparty/marginalia/internal/server"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// Options is what a review was asked for. A struct rather than a row of
// positional booleans, which is how open and watch get swapped by accident.
type Options struct {
	Host   string
	Port   int
	Open   bool
	Watch  bool
	Author string
	Config string // review config file, "" for the default review
	Review string // shipped preset name, "" for none
	Theme  string // palette name, "" for the default
	// Diagrams is "auto" (draw mermaid when a renderer is installed) or
	// "off" (always show the anchored source).
	Diagrams string
	MMDC     string    // explicit mermaid-cli path
	Log      io.Writer // startup notices; nil means stdout
}

// Build resolves the paths into a review set — one document, several, or a
// directory of them — parses each and wires it to its own feedback log.
func Build(paths []string, opts Options) (*server.Server, error) {
	set, err := reviewset.Load(paths)
	if err != nil {
		return nil, RouteSetError(err)
	}
	cfg, err := ReviewConfig(opts.Review, opts.Config)
	if err != nil {
		return nil, err
	}
	// A theme nobody has written yet is a feature request, not a typo.
	theme, err := web.ThemeFor(opts.Theme)
	if err != nil {
		return nil, Advertise(err)
	}
	drawer, err := Diagrams(opts.Diagrams, opts.MMDC)
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
		author = DefaultAuthor()
	}
	return server.New(server.Options{
		Docs:     docs,
		Title:    Title(set),
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
		Log:      opts.Log,
	}), nil
}

// Diagrams resolves the diagram-rendering choice. "auto" draws mermaid when
// mermaid-cli is installed and shows the anchored source when it is not, so a
// machine without Node still serves every review; "off" never draws.
//
// Naming a renderer is different from having one found for you: a --mmdc (or
// MARGINALIA_MMDC) that points nowhere is a typo, and falling back to "no
// renderer installed" would answer it with a notice about installing the thing
// the caller just said they had.
func Diagrams(mode, bin string) (*diagram.Renderer, error) {
	switch mode {
	case "", "auto":
		r := diagram.Find(bin)
		if named := firstNonEmpty(bin, os.Getenv("MARGINALIA_MMDC")); named != "" && !r.Available() {
			return nil, Advertise(fmt.Errorf("no mermaid renderer at %q — drop the flag to look on PATH, or pass --diagrams=off", named))
		}
		return r, nil
	case "off":
		return &diagram.Renderer{}, nil
	}
	return nil, Advertise(fmt.Errorf("unknown --diagrams %q — available: auto, off", mode))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ReviewConfig resolves the framing a review runs under: a shipped preset, a
// file, or both — the file layered over the preset, so an agent can take the
// vocabulary that already exists and change only the instructions. Neither is
// the plain default review.
func ReviewConfig(preset, path string) (*review.Config, error) {
	cfg := review.Default()
	if preset != "" {
		var err error
		if cfg, err = review.Preset(preset); err != nil {
			return nil, Advertise(err)
		}
	}
	if path != "" {
		if err := cfg.Overlay(path); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// PresetList names the shipped framings, for flag and tool help.
func PresetList() string { return strings.Join(review.PresetNames(), ", ") }

// ThemeList names the palettes, for flag and tool help.
func ThemeList() string { return strings.Join(web.ThemeNames(), ", ") }

// Title is what the page header calls the review: the index's title, or the
// directory the set was found in. A single document titles itself.
func Title(set *reviewset.Set) string {
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

// DefaultAuthor names the reviewer when nobody said who they are.
func DefaultAuthor() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "reviewer"
}
