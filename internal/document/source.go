package document

import (
	"strings"

	"github.com/gruesomeparty/marginalia/internal/codetree"
)

// parseSource turns a source file into a tree of its declarations,
// anchored by symbol path (`Batch/Full`, `const/batchCap`) as codetree
// derives it, in the order the file declares them.
//
// A declaration quotes and hashes what it says itself: a function all of
// it, signature and body, since a note on a function is nearly always about
// its body; a struct or class its header and closing, not its members, which
// are blocks of their own — so editing a method leaves a note on its type
// alone, the rule proto messages and HCL blocks already follow. Nothing in
// a path is a line number, so an edit elsewhere in the file moves nothing.
func parseSource(path string, src []byte, lang *codetree.Lang) (*Document, error) {
	tb := newTreeBuilder()
	f, err := codetree.Parse(lang, src)
	if err != nil {
		// No tree at all: show the file whole rather than refuse it.
		text := string(src)
		tb.add(node{ID: "@source", Kind: codetree.KindUnknown, Text: text, HTML: treeLine("", esc(text), "", "decl")})
	} else {
		addSymbols(tb, src, f, f.Symbols, "", 0)
	}
	if len(tb.blocks) == 0 {
		const empty = "This file contains no code."
		tb.add(node{ID: "@empty", Kind: "note", Text: empty, HTML: treeLine("", esc(empty), "", "decl")})
	}
	return &Document{Path: path, Format: FormatSource, Blocks: tb.blocks}, nil
}

func addSymbols(tb *treeBuilder, src []byte, f *codetree.File, syms []*codetree.Symbol, parent string, level int) {
	for _, s := range syms {
		head := s.Head()
		for head > s.Start && isSpaceByte(src[head-1]) {
			head--
		}
		text := string(src[s.Start:s.Head()]) + string(src[s.Tail():s.End])
		id := tb.add(node{
			Parent: parent,
			ID:     s.Path,
			Kind:   s.Kind,
			Level:  level,
			Text:   text,
			HTML:   treeLine("", f.HTML(src, s.Start, head), "", "decl"),
		})
		addSymbols(tb, src, f, s.Children, id, level+1)
	}
}

func isSpaceByte(c byte) bool { return strings.IndexByte(" \t\r\n\f\v", c) >= 0 }
