package web

import (
	"embed"
	"encoding/json"
	"html/template"
	"io"

	"github.com/suTerminus/marginalia/internal/document"
	"github.com/suTerminus/marginalia/internal/feedback"
)

//go:embed review.html.tmpl
var files embed.FS

var tmpl = template.Must(template.New("review.html.tmpl").
	Funcs(template.FuncMap{"safe": func(s string) template.HTML { return template.HTML(s) }}).
	ParseFS(files, "review.html.tmpl"))

type payload struct {
	Doc    *document.Document `json:"doc"`
	Events []feedback.Event   `json:"events"`
	Author string             `json:"author"`
}

type viewData struct {
	Doc      *document.Document
	Author   string
	DataJSON template.JS
}

// Render writes the self-contained review page.
func Render(w io.Writer, doc *document.Document, events []feedback.Event, author string) error {
	if events == nil {
		events = []feedback.Event{}
	}
	raw, err := json.Marshal(payload{Doc: doc, Events: events, Author: author})
	if err != nil {
		return err
	}
	return tmpl.Execute(w, viewData{Doc: doc, Author: author, DataJSON: template.JS(raw)})
}
