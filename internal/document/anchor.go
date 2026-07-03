package document

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func quote(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

func headingLevel(n ast.Node) int {
	if h, ok := n.(*ast.Heading); ok {
		return h.Level
	}
	return 0
}

func kindOf(n ast.Node) string {
	switch n.Kind() {
	case ast.KindHeading:
		return "heading"
	case ast.KindParagraph:
		return "paragraph"
	case ast.KindList:
		return "list"
	case ast.KindFencedCodeBlock, ast.KindCodeBlock:
		return "code"
	case ast.KindBlockquote:
		return "blockquote"
	case ast.KindThematicBreak:
		return "thematic_break"
	case east.KindTable:
		return "table"
	}
	return strings.ToLower(n.Kind().String())
}

// nodeText extracts the plain text of a block node.
func nodeText(n ast.Node, source []byte) string {
	switch n.Kind() {
	case ast.KindFencedCodeBlock, ast.KindCodeBlock:
		var sb strings.Builder
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			sb.Write(seg.Value(source))
		}
		return sb.String()
	}
	var sb strings.Builder
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *ast.Text:
			sb.Write(t.Segment.Value(source))
			if t.SoftLineBreak() || t.HardLineBreak() {
				sb.WriteByte(' ')
			}
		case *ast.String:
			sb.Write(t.Value)
		case *ast.AutoLink:
			sb.Write(t.URL(source))
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}

// sectioner tracks heading counters to produce hierarchical section paths.
type sectioner struct {
	counters [7]int
	section  string
	ordinal  int
}

func newSectioner() *sectioner { return &sectioner{section: "0"} }

func (s *sectioner) next(n ast.Node) (section string, ordinal int) {
	if h, ok := n.(*ast.Heading); ok {
		lvl := h.Level
		if lvl < 1 {
			lvl = 1
		}
		if lvl > 6 {
			lvl = 6
		}
		s.counters[lvl]++
		for i := lvl + 1; i <= 6; i++ {
			s.counters[i] = 0
		}
		parts := make([]string, 0, lvl)
		for i := 1; i <= lvl; i++ {
			parts = append(parts, strconv.Itoa(s.counters[i]))
		}
		s.section = strings.Join(parts, ".")
		s.ordinal = 0
	}
	s.ordinal++
	return s.section, s.ordinal
}
