package document

import (
	"strings"

	"github.com/gruesomeparty/marginalia/internal/hcl"
	"github.com/gruesomeparty/marginalia/internal/highlight"
)

var hclLang, _ = highlight.For("hcl")

// parseHCL turns a Terraform or other HCL file into a tree anchored by the
// path its blocks already spell: a block by its type and labels
// (`resource.aws_s3_bucket.logs`), what is inside it after a slash
// (`resource.aws_s3_bucket.logs/versioning/enabled`). A block quotes and
// hashes its own header, as a proto message does, so editing one attribute
// marks that attribute's note stale and leaves the block's alone; and since
// no id carries a line number, an edit elsewhere in the file moves nothing.
func parseHCL(path string, src []byte) (*Document, error) {
	tb := newTreeBuilder()
	addHCLNodes(tb, hcl.Parse(src), "", 0)
	if len(tb.blocks) == 0 {
		const empty = "This file contains no blocks or attributes."
		tb.add(node{ID: "@empty", Kind: "note", Text: empty, HTML: treeLine("", esc(empty), "", "decl")})
	}
	return &Document{Path: path, Format: FormatHCL, Blocks: tb.blocks}, nil
}

func addHCLNodes(tb *treeBuilder, nodes []*hcl.Node, parent string, level int) {
	for _, n := range nodes {
		id := tb.add(node{
			Parent: parent,
			ID:     join(parent, "/", hclName(n)),
			Kind:   n.Kind,
			Level:  level,
			Text:   strings.Join(nonEmpty(n.Comment, n.Text, n.Trailing), " "),
			HTML:   treeLine(n.Comment, hclLang.HTML(n.Text), n.Trailing, "decl"),
		})
		addHCLNodes(tb, n.Children, id, level+1)
	}
}

// hclName is a node's own segment of its id: a block's type and labels
// joined by dots, an attribute's name, or its kind for a comment or a line
// that could not be read — treeBuilder numbers repeats.
func hclName(n *hcl.Node) string {
	switch n.Kind {
	case hcl.KindBlock:
		return strings.Join(append([]string{n.Type}, n.Labels...), ".")
	case hcl.KindAttribute:
		return n.Type
	}
	return n.Kind
}
