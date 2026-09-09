package document

import (
	"fmt"
	"html"
	"strings"
)

// node describes one tree-shaped block before it is turned into a Block.
type node struct {
	Parent string // ID of the enclosing block ("" at the top level)
	ID     string // proposed ID; made unique by treeBuilder.add
	Kind   string
	Level  int    // nesting depth, 0 at the top level
	Text   string // plain text of this node alone — what the hash covers
	HTML   string // rendered line, already escaped
}

// treeBuilder accumulates blocks for tree-shaped documents (proto schemas,
// JSON/YAML/TOML data) in pre-order, so a parent always precedes its
// descendants — the order the renderer's indentation and collapse controls
// assume. It keeps IDs unique and marks which blocks have children.
type treeBuilder struct {
	blocks []Block
	index  map[string]int // block ID -> position in blocks
	kids   map[string]int // parent ID -> children added so far
}

func newTreeBuilder() *treeBuilder {
	return &treeBuilder{index: map[string]int{}, kids: map[string]int{}}
}

// add appends one block and returns the ID it was actually given, which the
// caller must use as the Parent of that block's children.
func (t *treeBuilder) add(n node) string {
	id := t.unique(n.ID)
	t.kids[n.Parent]++
	if i, ok := t.index[n.Parent]; ok {
		t.blocks[i].HasChildren = true
	}
	t.index[id] = len(t.blocks)
	t.blocks = append(t.blocks, Block{
		ID:        id,
		Section:   n.Parent,
		Parent:    n.Parent,
		Ordinal:   t.kids[n.Parent],
		Kind:      n.Kind,
		Level:     n.Level,
		Quote:     quote(normalize(n.Text), 90),
		Hash:      hashText(normalize(n.Text)),
		HTML:      n.HTML,
		PlainText: normalize(n.Text),
	})
	return id
}

// unique keeps IDs collision-free — duplicate JSON keys and repeated proto
// declarations are both legal enough to reach us — without disturbing the
// common case, where the derived path is already unique.
func (t *treeBuilder) unique(id string) string {
	if id == "" {
		id = "node"
	}
	if _, taken := t.index[id]; !taken {
		return id
	}
	for n := 2; ; n++ {
		alt := fmt.Sprintf("%s#%d", id, n)
		if _, taken := t.index[alt]; !taken {
			return alt
		}
	}
}

func esc(s string) string { return html.EscapeString(s) }

// treeLine renders one tree node where its author wrote it: a leading comment
// above the declaration, a trailing comment after it. decl is HTML and must
// already be escaped; the comments are plain text. The wrapper span leaves the
// block's first child free for the fold control the page adds.
func treeLine(comment, decl, trailing, class string) string {
	var b strings.Builder
	b.WriteString(`<span class="body">`)
	if comment != "" {
		b.WriteString(`<span class="cmt">`)
		b.WriteString(esc(comment))
		b.WriteString(`</span>`)
	}
	b.WriteString(`<code class="`)
	b.WriteString(class)
	b.WriteString(`">`)
	b.WriteString(decl)
	b.WriteString(`</code>`)
	if trailing != "" {
		b.WriteString(`<span class="cmt trail">`)
		b.WriteString(esc(trailing))
		b.WriteString(`</span>`)
	}
	b.WriteString(`</span>`)
	return b.String()
}
