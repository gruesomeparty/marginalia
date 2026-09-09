package document

import (
	"fmt"
	"regexp"
	"strings"
)

// Node kinds for data documents.
const (
	kindObject = "object"
	kindArray  = "array"
	kindString = "string"
	kindNumber = "number"
	kindBool   = "bool"
	kindNull   = "null"
)

// dataNode is one node of a parsed data document (JSON, YAML, TOML), kept in
// source order. Each format's parser produces these; the walk from here on —
// paths, anchors, rendering — is shared.
type dataNode struct {
	Key      string // map key, meaningful only when Keyed
	Keyed    bool   // true for object members; false for sequence elements
	Kind     string
	Value    string // rendered scalar; empty for containers
	Comment  string // comment above the node (YAML)
	Trailing string // comment after the node on its own line (YAML)
	Children []dataNode
}

// identKey matches keys that can be written with dot notation; anything else
// is bracket-quoted, so `$.a.b` and `$["odd key"].b` are both unambiguous.
var identKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// dataPath extends parent with one step: a key for object members, an index
// for sequence elements — the JSONPath-shaped anchor from issue #2
// (`$.spec.storage.paths[2]`).
func dataPath(parent string, n dataNode, index int) string {
	key := n.Key
	if !n.Keyed {
		return fmt.Sprintf("%s[%d]", parent, index)
	}
	if identKey.MatchString(key) {
		return parent + "." + key
	}
	return fmt.Sprintf("%s[%q]", parent, key)
}

// dataLabel is how a node is labelled on its own line: its key, or its index
// when it is a sequence element.
func dataLabel(n dataNode, index int) string {
	if !n.Keyed {
		return fmt.Sprintf("[%d]", index)
	}
	return n.Key
}

// dataText renders a node's own line as plain text — the text the anchor
// quotes and hashes. Containers show their shape rather than their contents,
// so editing a child never disturbs its parent's anchor beyond the child
// count.
func dataText(n dataNode, label string) string {
	value := n.Value
	switch n.Kind {
	case kindObject:
		value = "{" + countLabel(len(n.Children), "key", "keys") + "}"
	case kindArray:
		value = "[" + countLabel(len(n.Children), "item", "items") + "]"
	case kindString:
		value = fmt.Sprintf("%q", n.Value)
	}
	if label == "" {
		return value
	}
	return label + ": " + value
}

func countLabel(n int, one, many string) string {
	if n == 0 {
		return ""
	}
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// dataHTML renders a node's line, colouring the label apart from the value so
// a deep tree stays scannable.
func dataHTML(n dataNode, label string) string {
	var b strings.Builder
	if label != "" {
		b.WriteString(`<span class="key">`)
		b.WriteString(esc(label))
		b.WriteString(`</span><span class="punct">: </span>`)
	}
	switch n.Kind {
	case kindObject:
		b.WriteString(bracket("{", countLabel(len(n.Children), "key", "keys"), "}"))
	case kindArray:
		b.WriteString(bracket("[", countLabel(len(n.Children), "item", "items"), "]"))
	case kindString:
		b.WriteString(esc(fmt.Sprintf("%q", n.Value)))
	default:
		b.WriteString(esc(n.Value))
	}
	return treeLine(n.Comment, b.String(), n.Trailing, "val")
}

func bracket(open, count, close string) string {
	return `<span class="punct">` + open + `</span><span class="meta">` +
		esc(count) + `</span><span class="punct">` + close + `</span>`
}

// dataDocument turns parsed roots into a Document. Multi-document YAML keeps
// one root per document, distinguished in the path.
func dataDocument(path, format string, roots []dataNode) *Document {
	tb := newTreeBuilder()
	for i, root := range roots {
		id := "$"
		if len(roots) > 1 {
			id = fmt.Sprintf("$doc[%d]", i)
		}
		addDataNode(tb, root, id, "", "", 0)
	}
	return &Document{Path: path, Format: format, Blocks: tb.blocks}
}

// addDataNode appends n and its descendants in pre-order. label is what the
// node is called on its own line ("" for a root, which shows only its shape).
func addDataNode(tb *treeBuilder, n dataNode, id, parentID, label string, level int) {
	id = tb.add(node{
		Parent: parentID,
		ID:     id,
		Kind:   n.Kind,
		Level:  level,
		Text:   strings.Join(nonEmpty(n.Comment, dataText(n, label), n.Trailing), " "),
		HTML:   dataHTML(n, label),
	})
	for i, child := range n.Children {
		addDataNode(tb, child, dataPath(id, child, i), id, dataLabel(child, i), level+1)
	}
}
