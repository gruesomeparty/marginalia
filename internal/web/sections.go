package web

import (
	"strings"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// clientSection is one entry of the page's outline (issue #132): a top-level
// section of a markdown document, or a top-level node of a tree. It is a
// display grouping only. What counts as answered is still decided per block,
// so a section's totals are its blocks' totals and they sum to the document's.
type clientSection struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// sections assigns every block to a section and returns the sections in
// document order. It returns no sections when there would be fewer than two:
// a one-entry outline says nothing the progress bar does not.
func sections(doc *document.Document) ([]string, []clientSection) {
	var of []string
	if doc.IsTree() {
		of = treeSections(doc.Blocks)
	} else {
		of = proseSections(doc.Blocks)
	}
	var out []clientSection
	seen := map[string]bool{}
	for _, key := range of {
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, clientSection{ID: key, Label: label(doc, key)})
	}
	if len(out) < 2 {
		return nil, nil
	}
	return of, out
}

// proseSections groups markdown blocks by the outermost heading level that
// splits the document: a lone H1 title over H2 chapters groups by chapter,
// and a document written only in H2s still gets an outline. A block's
// Section is its heading path ("1.2"); the group is that path cut to the
// chosen depth. Prose before the first heading, or a title above the
// chapters, is a group of its own.
func proseSections(blocks []document.Block) []string {
	perLevel := map[int]int{}
	for _, b := range blocks {
		if b.Kind == "heading" {
			perLevel[b.Level]++
		}
	}
	depth := 0
	for l := 1; l <= 6; l++ {
		if perLevel[l] >= 2 {
			depth = l
			break
		}
	}
	of := make([]string, len(blocks))
	for i, b := range blocks {
		parts := strings.Split(b.Section, ".")
		if depth > 0 && len(parts) > depth {
			parts = parts[:depth]
		}
		of[i] = strings.Join(parts, ".")
	}
	return of
}

// treeSections groups tree blocks by their top-level node: a file of a diff,
// a message of a schema, a key of a JSON or YAML root. A tree with a single
// root (JSON's "$") is grouped one level down, or everything would be one
// section; the root itself stands alone.
func treeSections(blocks []document.Block) []string {
	parent := make(map[string]string, len(blocks))
	var roots []string
	for _, b := range blocks {
		parent[b.ID] = b.Parent
		if b.Parent == "" {
			roots = append(roots, b.ID)
		}
	}
	under := ""
	if len(roots) == 1 {
		under = roots[0]
	}
	of := make([]string, len(blocks))
	for i, b := range blocks {
		id := b.ID
		for depth := 0; depth < len(blocks); depth++ {
			p := parent[id]
			if p == "" || p == under {
				break
			}
			id = p
		}
		of[i] = id
	}
	return of
}

// label names a section: its heading for prose, its path for a tree.
func label(doc *document.Document, key string) string {
	if doc.IsTree() {
		return key
	}
	for _, b := range doc.Blocks {
		if b.Kind == "heading" && b.Section == key {
			return b.PlainText
		}
	}
	return "Before the first heading"
}
