package document

import (
	"fmt"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/highlight"
	"github.com/gruesomeparty/marginalia/internal/sqlstmt"
	"github.com/yuin/goldmark/ast"
)

var sqlLang, _ = highlight.For("sql")

// parseSQL turns a .sql file into one block per statement, anchored by
// ordinal, kind and what it acts on: `3:alter-table:orders`. A migration is
// read in order, so the ordinal is what places a statement; the kind and
// object make the id say what it is about. Inserting a statement before
// another moves its ordinal, so an old note orphans instead of landing on
// the newcomer — and where the newcomer happens to share kind and object,
// the hash marks the note stale. Either way it never misanchors.
func parseSQL(path string, src []byte) (*Document, error) {
	tb := newTreeBuilder()
	for i, s := range sqlstmt.Split(src) {
		tb.add(node{
			ID:   sqlID(i+1, s),
			Kind: s.Kind,
			Text: sqlText(s),
			HTML: treeLine(s.Comment, sqlLang.HTML(s.Text), s.Trailing, "decl"),
		})
	}
	// A page with no blocks has nothing to show or answer, so an empty file
	// says so rather than rendering blank.
	if len(tb.blocks) == 0 {
		const empty = "This file contains no statements."
		tb.add(node{ID: "@empty", Kind: "note", Text: empty, HTML: treeLine("", esc(empty), "", "decl")})
	}
	return &Document{Path: path, Format: FormatSQL, Blocks: tb.blocks}, nil
}

// sqlID is a statement's anchor: `3:alter-table:orders`, or `1:begin` for
// one that names nothing.
func sqlID(ordinal int, s sqlstmt.Stmt) string {
	id := fmt.Sprintf("%d:%s", ordinal, s.Kind)
	if s.Object != "" {
		id += ":" + s.Object
	}
	return id
}

// sqlText is what a statement's anchor quotes and hashes: the statement and
// the comments that document it, above and trailing.
func sqlText(s sqlstmt.Stmt) string {
	return strings.Join(nonEmpty(s.Comment, s.Text, s.Trailing), " ")
}

// isSQLFence reports whether a fenced code block holds SQL.
func isSQLFence(fence *ast.FencedCodeBlock, src []byte) bool {
	lang, ok := highlight.For(string(fence.Language(src)))
	return ok && lang.Name == "sql"
}

// sqlFence annotates a ```sql fence so each statement is its own inline
// block, the way a mermaid fence's statements are: the `<pre>` stays the
// source as written, highlighted, with each statement wrapped where it
// stands. The fence keeps its own block, so a note about the whole snippet
// still has a home. ok is false for a fence of fewer than two statements,
// where an anchor per statement would only duplicate the fence's own, and
// the caller renders it as the code block it always was.
func (p *mdParser) sqlFence(block Block, fence *ast.FencedCodeBlock) (ok bool) {
	code := fenceSource(fence, p.src)
	stmts := sqlstmt.Split([]byte(code))
	if len(stmts) < 2 {
		return false
	}
	var b strings.Builder
	b.WriteString(`<pre><code class="language-`)
	b.WriteString(esc(strings.ToLower(strings.TrimSpace(string(fence.Language(p.src))))))
	b.WriteString(`">`)
	at := 0
	var kids []Block
	for i, s := range stmts {
		plain := normalize(sqlText(s))
		child := Block{
			ID:        join(block.ID, "/", sqlID(i+1, s)),
			Section:   block.Section,
			Ordinal:   i + 1,
			Kind:      s.Kind,
			Level:     1,
			Parent:    block.ID,
			Inline:    true,
			Quote:     quote(plain, 90),
			Hash:      hashText(plain),
			PlainText: plain,
		}
		kids = append(kids, child)
		b.WriteString(sqlLang.HTML(code[at:s.Start]))
		inlineAnchor(&b, child, false)
		b.WriteString(sqlLang.HTML(code[s.Start:s.End]))
		b.WriteString(`</span>`)
		at = s.End
	}
	b.WriteString(sqlLang.HTML(code[at:]))
	b.WriteString("</code></pre>")
	block.HTML = b.String()
	block.HasChildren = true
	p.blocks = append(p.blocks, block)
	p.blocks = append(p.blocks, kids...)
	return true
}
