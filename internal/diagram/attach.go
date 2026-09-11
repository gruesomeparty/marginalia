package diagram

import (
	"strings"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// Attach renders every diagram in a document and hangs the SVG on the block
// that holds it, with the block's own statements stamped onto the shapes. It
// runs after parsing rather than inside it: parsing is pure and must stay
// that way — a subprocess and a cache directory have no business in the thing
// that turns bytes into blocks.
//
// A diagram that fails to render is not an error for the page: the block
// keeps the anchored source it already had, and the failure is returned so
// the caller can say so once.
func Attach(doc *document.Document, r *Renderer) (rendered int, failures []error) {
	if doc == nil || !r.Available() {
		return 0, nil
	}
	for i := range doc.Blocks {
		block := &doc.Blocks[i]
		if strings.TrimSpace(block.Source) == "" {
			continue
		}
		svg, err := r.SVG(block.Source)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		svg, _ = Stamp(svg, anchorsOf(doc, block.ID))
		block.SVG = svg
		rendered++
	}
	return rendered, failures
}

// anchorsOf collects the statements drawn inside one diagram, named by the
// last segment of their path, which is what the diagram calls them.
//
// Where those statements sit depends on the document: a markdown fence owns
// the blocks under its own path, while a .mmd file is one diagram whose
// statements are the file's top-level blocks — the header block holds the
// source, not a path the rest hang off.
func anchorsOf(doc *document.Document, host string) []Anchor {
	whole := doc.Format == document.FormatMermaid
	var out []Anchor
	for _, b := range doc.Blocks {
		if b.ID == host {
			continue
		}
		if !whole && !strings.HasPrefix(b.ID, host+"/") {
			continue
		}
		name := b.ID
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		out = append(out, Anchor{Block: b.ID, Kind: b.Kind, Name: name})
	}
	return out
}
