package document

// Block is a commentable element of a document: a markdown block-level
// element, or one node of a tree-shaped document (a proto declaration, a
// JSON/YAML/TOML node).
type Block struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Ordinal int    `json:"ordinal"`
	Kind    string `json:"kind"`
	Level   int    `json:"level"`
	Quote   string `json:"quote"`
	Hash    string `json:"hash"`
	HTML    string `json:"html"`
	// Parent and HasChildren drive tree indentation and the collapse
	// controls, and relate a markdown list item to its list.
	Parent      string `json:"parent,omitempty"`
	HasChildren bool   `json:"has_children,omitempty"`
	// Inline marks a block whose markup already sits inside its parent's
	// HTML — a markdown list item, anchored where the document put it. The
	// page renders no element of its own for it.
	Inline    bool   `json:"inline,omitempty"`
	PlainText string `json:"text"`
}

// Document is a parsed source document.
type Document struct {
	Path   string  `json:"path"`
	Format string  `json:"format"`
	Blocks []Block `json:"blocks"`
}

// IsTree reports whether the document renders as an indented, collapsible
// tree rather than as prose.
func (d *Document) IsTree() bool { return d.Format != FormatMarkdown }
