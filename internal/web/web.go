package web

import (
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
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
	Doc     *document.Document
	Events  []feedback.Event
	Author  string
	Title   string   // session title; falls back to the document's path
	Docs    []NavDoc // the review set — one entry means no navigation tree
	Nested  bool     // group the tree by directory (a discovered set)
	SetDone bool     // every document already carries a review_done
}

type payload struct {
	Doc    *document.Document `json:"doc"`
	Events []feedback.Event   `json:"events"`
	Author string             `json:"author"`
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
	Doc      *document.Document
	Title    string
	Author   string
	DataJSON template.JS
	Nav      []*navNode
	ShowNav  bool
	Total    int
	DoneN    int
	SetDone  bool
}

// Render writes the self-contained review page.
func Render(w io.Writer, p Page) error {
	events := p.Events
	if events == nil {
		events = []feedback.Event{}
	}
	raw, err := json.Marshal(payload{Doc: p.Doc, Events: events, Author: p.Author})
	if err != nil {
		return err
	}
	title := p.Title
	if title == "" {
		title = p.Doc.Path
	}
	done := 0
	for _, d := range p.Docs {
		if d.Done {
			done++
		}
	}
	return tmpl.Execute(w, viewData{
		Doc:      p.Doc,
		Title:    title,
		Author:   p.Author,
		DataJSON: template.JS(raw),
		Nav:      buildNav(p.Docs, p.Nested),
		ShowNav:  len(p.Docs) > 1,
		Total:    len(p.Docs),
		DoneN:    done,
		SetDone:  p.SetDone,
	})
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
