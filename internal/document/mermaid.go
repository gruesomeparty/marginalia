package document

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/mermaid"
	"github.com/yuin/goldmark/ast"
)

// mermaidSourceKind is the block kind of a diagram Marginalia could not take
// apart: the whole source, still reviewable as one block.
const mermaidSourceKind = "source"

// parseMermaid turns a .mmd/.mermaid file into blocks anchored by statement —
// `client-->api` for an edge, `payments/worker-->queue` for one inside a
// subgraph — so a note lands on the edge that is wrong rather than on the
// diagram that contains it.
//
// A diagram type the parser does not take apart (a sequence diagram, say) is
// not an error: it renders as the single source block it is today, which is
// exactly what the reviewer had before.
func parseMermaid(path string, src []byte) (*Document, error) {
	dia, err := mermaid.Parse(src)
	if err != nil {
		if errors.Is(err, mermaid.ErrUnsupported) {
			return mermaidSource(path, src), nil
		}
		return nil, err
	}
	tb := newTreeBuilder()
	addMermaidStmts(tb, dia.Stmts, "", 0)
	return &Document{Path: path, Format: FormatMermaid, Blocks: tb.blocks}, nil
}

// addMermaidStmts walks statements in source order; a subgraph's contents hang
// off its path with a slash, the way a proto message's fields do.
func addMermaidStmts(tb *treeBuilder, stmts []*mermaid.Stmt, scope string, level int) {
	for _, s := range stmts {
		id := tb.add(node{
			Parent: scope,
			ID:     join(scope, "/", s.Name),
			Kind:   s.Kind,
			Level:  level,
			Text:   mermaidText(s),
			HTML:   treeLine(s.Comment, esc(s.Text), "", "decl"),
		})
		addMermaidStmts(tb, s.Children, id, level+1)
	}
}

// mermaidText is what a statement's anchor quotes and hashes: what it says,
// including the comment that documents it.
func mermaidText(s *mermaid.Stmt) string {
	return strings.Join(nonEmpty(s.Comment, s.Text), " ")
}

// mermaidSource keeps an unparsed diagram whole and reviewable.
func mermaidSource(path string, src []byte) *Document {
	text := strings.TrimRight(string(src), "\n")
	tb := newTreeBuilder()
	tb.add(node{
		ID:   "diagram",
		Kind: mermaidSourceKind,
		Text: text,
		HTML: treeLine("", esc(text), "", "decl"),
	})
	return &Document{Path: path, Format: FormatMermaid, Blocks: tb.blocks}
}

// isMermaidFence reports whether a fenced code block holds a mermaid diagram.
func isMermaidFence(fence *ast.FencedCodeBlock, src []byte) bool {
	switch strings.ToLower(string(fence.Language(src))) {
	case "mermaid", "mmd":
		return true
	}
	return false
}

// mermaidFence annotates a ```mermaid fence so every statement in it is its
// own anchorable block, the way list items are (#12): the `<pre>` stays the
// source the author wrote — nothing reordered, nothing dropped — with each
// statement wrapped where it stands.
//
// The fence keeps the block ID it always had, so a note about the diagram as
// a whole ("this flow is upside down") still has somewhere to live, and
// feedback written before statement anchoring existed still anchors. ok is
// false for a diagram type the parser does not take apart, which leaves the
// fence to goldmark and the reviewer with the block they have today.
func (p *mdParser) mermaidFence(fence *ast.FencedCodeBlock, section, parentID string) (html string, kids, ok bool) {
	code := fenceSource(fence, p.src)
	dia, err := mermaid.Parse([]byte(code))
	if err != nil {
		return "", false, false
	}
	taken := map[string]bool{}
	var b strings.Builder
	b.WriteString(`<pre><code class="language-mermaid">`)
	at := 0
	for _, fb := range flattenMermaid(dia.Stmts, parentID, 1, taken) {
		s := fb.stmt
		// Anchors annotate the source in place: a range that would reorder or
		// overlap what the author wrote is left as plain text instead.
		if s.Start < at || s.End > len(code) || s.End <= s.Start {
			continue
		}
		plain := normalize(mermaidText(s))
		p.blocks = append(p.blocks, Block{
			ID:      fb.id,
			Section: section,
			Ordinal: fb.ordinal,
			Kind:    s.Kind,
			Level:   fb.level,
			Parent:  fb.parent,
			// The markup lives inside the fence's own <pre>, so the page
			// renders no element of its own for it.
			Inline:      true,
			HasChildren: len(s.Children) > 0,
			Quote:       quote(plain, 90),
			Hash:        hashText(plain),
			PlainText:   plain,
		})
		b.WriteString(esc(code[at:s.Start]))
		fmt.Fprintf(&b, `<span class="block" data-block="%s" data-hash="%s" data-quote="%s" data-kind="%s" data-level="%d" data-parent="%s"%s tabindex="0" role="button" aria-label="Block %s">`,
			esc(fb.id), esc(hashText(plain)), esc(quote(plain, 90)), esc(s.Kind),
			fb.level, esc(fb.parent), kidsAttr(len(s.Children) > 0), esc(fb.id))
		b.WriteString(esc(code[s.Start:s.End]))
		b.WriteString(`</span>`)
		at = s.End
		kids = true
	}
	b.WriteString(esc(code[at:]))
	b.WriteString("</code></pre>")
	return b.String(), kids, true
}

func kidsAttr(has bool) string {
	if has {
		return ` data-kids="1"`
	}
	return ""
}

// fenceStmt is one statement of a fenced diagram with the anchor it was given.
type fenceStmt struct {
	stmt    *mermaid.Stmt
	id      string
	parent  string
	level   int
	ordinal int
}

// flattenMermaid pairs each statement with its block ID in source order, so
// one pass over the fence's source can annotate it. The header is left out:
// the fence itself anchors the diagram, and a second anchor over the same
// words would only split notes between them.
func flattenMermaid(stmts []*mermaid.Stmt, scope string, level int, taken map[string]bool) []fenceStmt {
	var out []fenceStmt
	for i, s := range stmts {
		if s.Kind == mermaid.KindDiagram {
			continue
		}
		id := uniqueID(join(scope, "/", s.Name), func(id string) bool { return taken[id] })
		taken[id] = true
		out = append(out, fenceStmt{stmt: s, id: id, parent: scope, level: level, ordinal: i + 1})
		out = append(out, flattenMermaid(s.Children, id, level+1, taken)...)
	}
	return out
}

// fenceSource is a fenced block's code, as written.
func fenceSource(fence *ast.FencedCodeBlock, src []byte) string {
	var b strings.Builder
	lines := fence.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	return b.String()
}
