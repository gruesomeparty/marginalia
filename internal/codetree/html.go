package codetree

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Token classes: the CSS classes internal/highlight already gives fences,
// so a source file and a fenced snippet colour alike.
const (
	classKeyword = "kw"
	classString  = "str"
	classNumber  = "num"
	classComment = "com"
)

// colour classifies the tree's leaves. It reads node types rather than a
// highlight query, so it works the same for every grammar: comments,
// string and character literals, numbers, and the anonymous words a grammar
// spells literally (`func`, `return`, `class`), which are its keywords.
func (w *walker) colour(n *ts.Node) {
	typ := w.typ(n)
	if n.IsNamed() {
		if class := namedClass(typ); class != "" {
			w.span(n, class)
			return
		}
	}
	if n.ChildCount() == 0 {
		if !n.IsNamed() && isWord(typ) && w.text(n) == typ {
			w.span(n, classKeyword)
		}
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		w.colour(n.Child(i))
	}
}

func (w *walker) span(n *ts.Node, class string) {
	if s, e := int(n.StartByte()), int(n.EndByte()); s < e {
		w.spans = append(w.spans, Span{Start: s, End: e, Class: class})
	}
}

func namedClass(typ string) string {
	switch {
	case strings.Contains(typ, "comment"):
		return classComment
	case strings.Contains(typ, "string") || strings.Contains(typ, "char_literal") ||
		strings.Contains(typ, "rune_literal") || typ == "character":
		return classString
	case strings.Contains(typ, "int_literal") || strings.Contains(typ, "float_literal") ||
		strings.Contains(typ, "imaginary_literal") || strings.Contains(typ, "number") ||
		typ == "integer" || typ == "float":
		return classNumber
	case typ == "true" || typ == "false" || typ == "nil" || typ == "null" || typ == "none" || typ == "iota":
		return classKeyword
	}
	return ""
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}

// HTML renders src[from:to] as escaped HTML with the file's spans applied.
// Lines after the first lose the indentation of the line `from` starts on,
// so a method read out of a class body reads flush rather than indented by
// its position in the file; the tree already shows its depth — except inside
// a string literal, whose whitespace is its content. Stripping the tags gives
// back the source byte for byte, less that indentation.
func (f *File) HTML(src []byte, from, to int) string {
	if from < 0 || to > len(src) || from >= to {
		return ""
	}
	indent := 0
	for i := from - 1; i >= 0 && src[i] != '\n'; i-- {
		if src[i] != ' ' && src[i] != '\t' {
			indent = 0
			break
		}
		indent++
	}
	var b strings.Builder
	b.Grow((to - from) * 5 / 4)
	k := 0 // first span that may still apply
	open := ""
	skip := 0 // indentation still to drop on this line
	for i := from; i < to; i++ {
		c := src[i]
		for k < len(f.Spans) && f.Spans[k].End <= i {
			k++
		}
		class := ""
		if k < len(f.Spans) && f.Spans[k].Start <= i {
			class = f.Spans[k].Class
		}
		// Inside a multi-line string the indentation is the string's own.
		if skip > 0 && class != classString && (c == ' ' || c == '\t') {
			skip--
			continue
		}
		skip = 0
		if class != open {
			if open != "" {
				b.WriteString("</span>")
			}
			if class != "" {
				b.WriteString(`<span class="tok `)
				b.WriteString(class)
				b.WriteString(`">`)
			}
			open = class
		}
		switch c {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&#34;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteByte(c)
		}
		if c == '\n' {
			skip = indent
		}
	}
	if open != "" {
		b.WriteString("</span>")
	}
	return b.String()
}
