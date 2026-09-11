package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/review"
)

//go:embed review.html.tmpl
var files embed.FS

var tmpl = template.Must(template.New("review.html.tmpl").
	Funcs(template.FuncMap{"safe": func(s string) template.HTML { return template.HTML(s) }}).
	ParseFS(files, "review.html.tmpl"))

// NavDoc is one document of the review set as the navigation tree sees it.
type NavDoc struct {
	Label   string
	Rel     string
	Count   int
	Done    bool
	Current bool
}

// Page is everything one review page renders: the document being reviewed,
// its feedback so far, and the set it belongs to.
type Page struct {
	Doc        *document.Document
	Events     []feedback.Event
	Resolution feedback.Resolution
	Author     string
	Title      string   // session title; falls back to the document's path
	Docs       []NavDoc // the review set — one entry means no navigation tree
	Nested     bool     // group the tree by directory (a discovered set)
	SetDone    bool     // every document already carries a review_done
	Watch      bool     // the server re-parses on change: let the page notice
	Revision   uint64   // the re-parse this render was made from
	// Review is how the agent framed this review: instructions for the
	// reviewer, the vocabulary they answer in, and which blocks are
	// read-only. Nil means the default review.
	Review *review.Config
}

// clientBlock is the projection of a Block the page's script actually reads:
// enough to build a feedback event and to fold a tree. The rendered HTML is
// left out — it is already in the DOM, and shipping it twice doubled the page
// for no one's benefit.
type clientBlock struct {
	ID          string `json:"id"`
	Parent      string `json:"parent,omitempty"`
	HasChildren bool   `json:"has_children,omitempty"`
	Hash        string `json:"hash"`
	Quote       string `json:"quote"`
	Text        string `json:"text"`
	// ReadOnly blocks are shown but not up for comment. The page needs it per
	// block rather than as patterns: an inline block — a list item, a diagram
	// statement — has its markup written by the parser, so the page marks
	// those elements itself.
	ReadOnly bool `json:"readonly,omitempty"`
}

type clientDoc struct {
	Path   string        `json:"path"`
	Blocks []clientBlock `json:"blocks"`
}

type payload struct {
	Doc        clientDoc           `json:"doc"`
	Events     []feedback.Event    `json:"events"`
	Resolution feedback.Resolution `json:"resolution"`
	Author     string              `json:"author"`
	Watch      bool                `json:"watch"`
	Revision   uint64              `json:"revision"`
	Review     Review              `json:"review"`
}

// Review is the review configuration as the page and an API consumer see it:
// the framing, and the vocabulary an agent reading the log will need in order
// to know what a custom type was asked to mean.
type Review struct {
	Title          string          `json:"title,omitempty"`
	Instructions   string          `json:"instructions,omitempty"`
	Actions        []review.Action `json:"actions"`
	ReadOnly       []string        `json:"readonly,omitempty"`
	RequireVerdict bool            `json:"require_verdict,omitempty"`
}

// ReviewInfo projects a review config for the page and for GET /api/doc.
func ReviewInfo(c *review.Config) Review {
	if c == nil {
		c = review.Default()
	}
	return Review{
		Title:          c.Title,
		Instructions:   c.Instructions,
		Actions:        c.Actions(),
		ReadOnly:       c.ReadOnly,
		RequireVerdict: c.RequireVerdict,
	}
}

// project reduces a document to what the client needs. Kinds, levels and
// ordinals are omitted deliberately: the page reads those off the block
// element's data attributes.
func project(doc *document.Document, cfg *review.Config) clientDoc {
	out := clientDoc{Path: doc.Path, Blocks: make([]clientBlock, 0, len(doc.Blocks))}
	for _, b := range doc.Blocks {
		out.Blocks = append(out.Blocks, clientBlock{
			ID:          b.ID,
			Parent:      b.Parent,
			HasChildren: b.HasChildren,
			Hash:        b.Hash,
			Quote:       b.Quote,
			Text:        b.PlainText,
			ReadOnly:    cfg.Locked(b.ID),
		})
	}
	return out
}

// navNode is a rendered navigation entry: either a directory heading or a
// document link.
type navNode struct {
	Label    string
	URL      string
	Dir      bool
	Count    int
	Done     bool
	Current  bool
	Children []*navNode
}

type viewData struct {
	Doc         *document.Document
	Orphans     []feedback.State
	OrphanTitle string
	Title       string
	DataJSON    template.JS
	Nav         []*navNode
	ShowNav     bool
	Total       int
	DoneN       int
	SetDone     bool
	Review      Review
}

// Render writes the self-contained review page.
func Render(w io.Writer, p Page) error {
	events := p.Events
	if events == nil {
		events = []feedback.Event{}
	}
	cfg := p.Review
	if cfg == nil {
		cfg = review.Default()
	}
	info := ReviewInfo(cfg)
	raw, err := json.Marshal(payload{
		Doc: project(p.Doc, cfg), Events: events, Resolution: p.Resolution,
		Author: p.Author, Review: info, Watch: p.Watch, Revision: p.Revision,
	})
	if err != nil {
		return err
	}
	// A review config that names the review says what the page is called; a
	// set index's title, then the document's path, are the fallbacks.
	title := info.Title
	if title == "" {
		title = p.Title
	}
	if title == "" {
		title = p.Doc.Path
	}
	done := 0
	for _, d := range p.Docs {
		if d.Done {
			done++
		}
	}
	lost := orphans(p.Resolution)
	return tmpl.Execute(w, viewData{
		Doc:         p.Doc,
		Orphans:     lost,
		OrphanTitle: orphanTitle(len(lost)),
		Title:       title,
		DataJSON:    template.JS(raw),
		Nav:         buildNav(p.Docs, p.Nested),
		ShowNav:     len(p.Docs) > 1,
		Total:       len(p.Docs),
		DoneN:       done,
		SetDone:     p.SetDone,
		Review:      info,
	})
}

// orphans are the notes whose block no longer exists in the document. They
// are shown at the end of the page rather than dropped: a note the document
// has outrun is exactly what a second pass needs to see.
func orphans(res feedback.Resolution) []feedback.State {
	var out []feedback.State
	for _, s := range res.States {
		if s.Orphaned {
			out = append(out, s)
		}
	}
	return out
}

// orphanTitle names the unanchored pile without the template having to count.
func orphanTitle(n int) string {
	if n == 1 {
		return "1 note no longer anchored"
	}
	return fmt.Sprintf("%d notes no longer anchored", n)
}

// buildNav turns the review set into the sidebar's tree. A discovered set
// mirrors the folders it was found in; a curated one is exactly the list the
// index gave, in its order and under its labels — the index is the tree.
func buildNav(docs []NavDoc, nested bool) []*navNode {
	if len(docs) < 2 {
		return nil
	}
	if !nested {
		nodes := make([]*navNode, 0, len(docs))
		for _, d := range docs {
			nodes = append(nodes, leaf(d, d.Label))
		}
		return nodes
	}
	root := &navNode{Dir: true}
	for _, d := range docs {
		parts := strings.Split(d.Rel, "/")
		parent := root
		for _, dir := range parts[:len(parts)-1] {
			parent = child(parent, dir)
		}
		parent.Children = append(parent.Children, leaf(d, parts[len(parts)-1]))
	}
	return root.Children
}

// child finds or creates the directory node named dir under parent.
func child(parent *navNode, dir string) *navNode {
	for _, n := range parent.Children {
		if n.Dir && n.Label == dir {
			return n
		}
	}
	n := &navNode{Label: dir, Dir: true}
	parent.Children = append(parent.Children, n)
	return n
}

func leaf(d NavDoc, label string) *navNode {
	return &navNode{
		Label:   label,
		URL:     "/d/" + d.Rel,
		Count:   d.Count,
		Done:    d.Done,
		Current: d.Current,
	}
}
