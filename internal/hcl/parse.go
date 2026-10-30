// Package hcl is a tolerant, structural reader of HCL — Terraform's `.tf`
// and `.tfvars`, and any other `.hcl` file. Like protoschema it reads only as
// much as review needs: a body of attributes and blocks, nested, each with
// the comments that document it and the bytes it occupies. Expressions are
// skipped, not evaluated, but skipped correctly — through brackets, strings,
// `${…}` templates and heredocs — so a brace inside a policy document never
// ends a block. It never fails: what it cannot read becomes an Unknown node
// holding the line, so a broken file still has something to review.
//
// It is hand-written rather than hashicorp/hcl's hclsyntax on purpose: that
// parser refuses a file with a syntax error, which is exactly the file a
// reviewer may be handed, and it would be a heavy dependency for one format.
package hcl

import (
	"bytes"
	"strings"
)

// Node kinds.
const (
	KindBlock     = "block"
	KindAttribute = "attribute"
	KindComment   = "comment" // comments with nothing after them in a body
	KindUnknown   = "unknown"
)

// Node is one item of a body.
type Node struct {
	Kind     string
	Type     string   // a block's type or an attribute's name
	Labels   []string // a block's labels, unquoted
	Text     string   // a block's header up to its `{`; an attribute or unknown line whole
	Comment  string   // comments directly above
	Trailing string   // a comment after it on the same line
	Children []*Node  // a block's body
	Start    int      // byte offset of the node
	End      int      // byte offset just past it (a block's closing brace included)
}

// Parse reads src as an HCL body.
func Parse(src []byte) []*Node {
	p := &parser{src: src}
	nodes, _ := p.body(0, false)
	return nodes
}

type parser struct {
	src []byte
}

// body reads items from i until EOF or, when nested, the `}` that closes the
// enclosing block, and returns the offset just past what it consumed.
func (p *parser) body(i int, nested bool) ([]*Node, int) {
	var (
		nodes []*Node
		lead  []string
		start = -1 // where the pending comments began
	)
	flush := func(end int) {
		if len(lead) > 0 {
			nodes = append(nodes, &Node{Kind: KindComment, Text: strings.Join(lead, "\n"), Start: start, End: end})
		}
	}
	for {
		i = p.space(i, true)
		if i >= len(p.src) {
			flush(i)
			return nodes, i
		}
		if c, ok := p.comment(i); ok {
			if start < 0 {
				start = i
			}
			lead = append(lead, strings.TrimSpace(string(p.src[i:c])))
			i = c
			continue
		}
		if p.src[i] == '}' {
			if nested {
				flush(i)
				return nodes, i + 1
			}
			// A stray closer at the top level: keep it visible.
			n := &Node{Kind: KindUnknown, Text: "}", Start: i, End: i + 1}
			p.attach(n, lead)
			nodes, lead, start, i = append(nodes, n), nil, -1, i+1
			continue
		}
		n := p.item(i)
		p.attach(n, lead)
		nodes, lead, start, i = append(nodes, n), nil, -1, n.End
	}
}

func (p *parser) attach(n *Node, lead []string) {
	n.Comment = strings.Join(lead, "\n")
}

// item reads one attribute, block or unreadable line starting at i.
func (p *parser) item(i int) *Node {
	n := &Node{Start: i}
	j := p.ident(i)
	if j > i {
		n.Type = string(p.src[i:j])
		k := p.space(j, false)
		if k < len(p.src) && p.src[k] == '=' && (k+1 >= len(p.src) || p.src[k+1] != '=') {
			end := p.expr(k+1, true)
			n.Kind, n.Text = KindAttribute, p.text(i, end)
			n.End, n.Trailing = p.trailing(end)
			return n
		}
		// A block: labels, then `{`.
		var labels []string
		for k < len(p.src) {
			if p.src[k] == '"' {
				e := p.str(k)
				labels = append(labels, strings.TrimSuffix(string(p.src[k+1:e]), `"`))
				k = p.space(e, false)
				continue
			}
			if e := p.ident(k); e > k {
				labels = append(labels, string(p.src[k:e]))
				k = p.space(e, false)
				continue
			}
			break
		}
		if k < len(p.src) && p.src[k] == '{' {
			n.Kind, n.Labels, n.Text = KindBlock, labels, string(p.src[i:k+1])
			var after int
			after, n.Trailing = p.trailing(k + 1)
			n.Children, n.End = p.body(after, true)
			return n
		}
	}
	// Neither: keep the line, through any brackets it opens, as written. At
	// least one byte is consumed so a byte no rule reads cannot stall us.
	end := p.expr(i, true)
	if end == i {
		end = i + 1
	}
	n.Kind, n.Type, n.Text = KindUnknown, "", p.text(i, end)
	n.End, n.Trailing = p.trailing(end)
	return n
}

// text is src[i:end] trimmed, with continuation lines dedented by the
// column i starts at, so a multi-line value reads as it does in the file
// rather than indented twice over.
func (p *parser) text(i, end int) string {
	col := i - (bytes.LastIndexByte(p.src[:i], '\n') + 1)
	lines := strings.Split(strings.TrimSpace(string(p.src[i:end])), "\n")
	for k := 1; k < len(lines); k++ {
		l := lines[k]
		cut := 0
		for cut < col && cut < len(l) && (l[cut] == ' ' || l[cut] == '\t') {
			cut++
		}
		lines[k] = l[cut:]
	}
	return strings.Join(lines, "\n")
}

// trailing reads a comment that follows on the same line as i, returning
// where the item ends and the comment's text.
func (p *parser) trailing(i int) (int, string) {
	j := p.space(i, false)
	if c, ok := p.comment(j); ok {
		return c, strings.TrimSpace(string(p.src[j:c]))
	}
	return i, ""
}

// expr returns the end of the expression starting at i. With stopNL it ends
// at a newline, comment or unmatched closer at bracket depth zero — the end
// of an attribute; without, only at an unmatched closer — the end of a
// template interpolation.
func (p *parser) expr(i int, stopNL bool) int {
	depth := 0
	for i < len(p.src) {
		c := p.src[i]
		switch {
		case c == '"':
			i = p.str(i)
			continue
		case c == '<' && bytes.HasPrefix(p.src[i:], []byte("<<")):
			if e, ok := p.heredoc(i); ok {
				i = e
				continue
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth == 0 {
				return i
			}
			depth--
		case c == '\n' && stopNL && depth == 0:
			return i
		default:
			if e, ok := p.comment(i); ok {
				if stopNL && depth == 0 {
					return i
				}
				i = e
				continue
			}
		}
		i++
	}
	return i
}

// str returns the offset just past the string opening at i. Templates are
// skipped as expressions, so `"${jsonencode({a = "}"})}"` is one string. A
// newline ends an unterminated string, so one stray quote cannot swallow
// the rest of the file.
func (p *parser) str(i int) int {
	i++
	for i < len(p.src) {
		switch c := p.src[i]; {
		case c == '"':
			return i + 1
		case c == '\n':
			return i
		case c == '\\':
			i += 2
			continue
		case (c == '$' || c == '%') && i+1 < len(p.src):
			if p.src[i+1] == c { // `$${` and `%%{` are escapes
				i += 2
				continue
			}
			if p.src[i+1] == '{' {
				i = p.expr(i+2, false)
				if i < len(p.src) {
					i++ // the closing brace
				}
				continue
			}
		}
		i++
	}
	return min(i, len(p.src))
}

// heredoc returns the offset of the newline that ends the heredoc opening at
// i (`<<EOT` or `<<-EOT`), or EOF if its terminator never comes.
func (p *parser) heredoc(i int) (int, bool) {
	j := i + 2
	if j < len(p.src) && p.src[j] == '-' {
		j++
	}
	e := p.ident(j)
	if e == j {
		return 0, false
	}
	marker := string(p.src[j:e])
	nl := bytes.IndexByte(p.src[e:], '\n')
	if nl < 0 {
		return len(p.src), true
	}
	for at := e + nl + 1; at < len(p.src); {
		end := bytes.IndexByte(p.src[at:], '\n')
		if end < 0 {
			end = len(p.src) - at
		}
		if strings.TrimSpace(string(p.src[at:at+end])) == marker {
			return at + end, true
		}
		at += end + 1
	}
	return len(p.src), true
}

// comment returns the end of a comment starting at i, if one does: `#` and
// `//` run to the end of the line, `/* */` to its close.
func (p *parser) comment(i int) (int, bool) {
	if i >= len(p.src) {
		return 0, false
	}
	rest := p.src[i:]
	switch {
	case rest[0] == '#' || bytes.HasPrefix(rest, []byte("//")):
		if nl := bytes.IndexByte(rest, '\n'); nl >= 0 {
			return i + nl, true
		}
		return len(p.src), true
	case bytes.HasPrefix(rest, []byte("/*")):
		if e := bytes.Index(rest[2:], []byte("*/")); e >= 0 {
			return i + 2 + e + 2, true
		}
		return len(p.src), true
	}
	return 0, false
}

// space skips blanks from i, and newlines too when nl is set.
func (p *parser) space(i int, nl bool) int {
	for i < len(p.src) {
		switch p.src[i] {
		case ' ', '\t', '\r':
		case '\n':
			if !nl {
				return i
			}
		default:
			return i
		}
		i++
	}
	return i
}

// ident returns the end of the identifier at i, or i if there is none.
func (p *parser) ident(i int) int {
	for i < len(p.src) {
		c := p.src[i]
		if c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80 {
			i++
			continue
		}
		break
	}
	return i
}
