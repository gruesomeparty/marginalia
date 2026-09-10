// Package mermaid parses mermaid diagram source structurally, so every
// statement of a flowchart — a node, an edge, a subgraph — can be anchored and
// commented on its own instead of the diagram being one opaque block of code.
//
// Like internal/protoschema the parser is deliberately tolerant rather than
// semantic: it does not validate, resolve or reject. A construct it does not
// recognize is still kept, verbatim, as a statement — losing a line of
// someone's diagram would be far worse than mislabelling it. Diagram types
// other than flowchart/graph return ErrUnsupported, which callers turn into
// "review this as source" rather than an error.
package mermaid

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
)

// ErrUnsupported reports source that is not a flowchart, so the caller can
// fall back to reviewing it as a single block of source.
var ErrUnsupported = errors.New("mermaid: only flowchart and graph diagrams are parsed statement by statement")

// Statement kinds produced by Parse.
const (
	KindDiagram     = "diagram"     // the `flowchart LR` header
	KindNode        = "node"        // `client[Client]`
	KindEdge        = "edge"        // `client --> api`
	KindSubgraph    = "subgraph"    // `subgraph payments [Payments]`
	KindFrontmatter = "frontmatter" // the leading `---` YAML block
	KindInit        = "init"        // a `%%{init: …}%%` directive
	KindComment     = "comment"     // `%%` lines with no statement to attach to
	KindStatement   = "statement"   // anything else, kept as written
)

// directives are the flowchart keywords that describe the diagram rather than
// its graph, mapped to the block kind they render as.
var directives = map[string]string{
	"direction": "direction",
	"classDef":  "class_def",
	"class":     "class",
	"style":     "style",
	"linkStyle": "link_style",
	"click":     "click",
	"accTitle":  "acc_title",
	"accDescr":  "acc_descr",
}

// Stmt is one statement of a diagram. Start and End are byte offsets into the
// source, covering the statement and any comment attached above it, so a
// renderer can annotate the source in place instead of rebuilding it.
type Stmt struct {
	Kind     string
	Name     string // path segment: `client`, `client-->api`, `payments`
	Text     string // the statement as written, trimmed
	Comment  string // `%%` comment lines directly above it, `%%` stripped
	Line     int    // 1-based line the statement itself starts on
	Start    int
	End      int
	Children []*Stmt // statements inside a subgraph
}

// Diagram is a parsed flowchart. Stmts is in source order and includes the
// header as its first statement, so a renderer that walks it emits the
// document as written.
type Diagram struct {
	Type  string // "flowchart" or "graph"
	Stmts []*Stmt
}

var headerRe = regexp.MustCompile(`(?i)^(flowchart|graph)\b`)

// Parse turns mermaid source into a diagram. It returns ErrUnsupported when
// the source does not open a flowchart — every other input parses, however
// odd, because the fallback is to keep the line as it was written.
func Parse(src []byte) (*Diagram, error) {
	p := &parser{lines: splitLines(src), dia: &Diagram{}}
	i := p.prelude()
	if i < 0 {
		return nil, ErrUnsupported
	}
	parts := splitStatements(p.lines[i])
	m := headerRe.FindStringSubmatch(parts[0].text)
	if m == nil {
		return nil, ErrUnsupported
	}
	p.dia.Type = strings.ToLower(m[1])
	p.add(&Stmt{
		Kind: KindDiagram, Name: p.dia.Type, Text: parts[0].text,
		Line: p.lines[i].num, Start: parts[0].start, End: parts[0].end,
	})
	// `graph TD; A-->B` is legal: the header's own line can carry statements.
	for _, part := range parts[1:] {
		p.statement(part, p.lines[i].num)
	}
	for i++; i < len(p.lines); i++ {
		ln := p.lines[i]
		body := strings.TrimSpace(ln.text)
		switch {
		case body == "":
		case isComment(body):
			p.comment(ln)
		case strings.HasPrefix(body, "%%{"):
			i = p.directive(i, KindInit, "init", "}%%")
		default:
			for _, part := range splitStatements(ln) {
				p.statement(part, ln.num)
			}
		}
	}
	p.flush()
	return p.dia, nil
}

// srcLine is one line of source with the offset it starts at, which is what
// makes in-place annotation of the original text possible.
type srcLine struct {
	text  string
	start int
	num   int
}

func splitLines(src []byte) []srcLine {
	lines := make([]srcLine, 0, bytes.Count(src, []byte{'\n'})+1)
	for at, num := 0, 1; ; num++ {
		end := bytes.IndexByte(src[at:], '\n')
		if end < 0 {
			return append(lines, srcLine{text: string(src[at:]), start: at, num: num})
		}
		lines = append(lines, srcLine{text: string(src[at : at+end]), start: at, num: num})
		at += end + 1
	}
}

// span is a statement's text with the offsets it occupies in the source.
type span struct {
	text       string
	start, end int
}

type parser struct {
	lines []srcLine
	dia   *Diagram
	stack []*Stmt // open subgraphs, innermost last
	// Comment lines wait here until the statement they describe shows up.
	pending  []string
	raw      []string
	pendAt   int
	pendEnd  int
	pendLine int
}

// prelude consumes what may legally precede the header — front matter,
// `%%{init}%%` directives, comments, blank lines — and returns the index of
// the header line, or -1 when the source never gets to one.
func (p *parser) prelude() int {
	for i := 0; i < len(p.lines); i++ {
		body := strings.TrimSpace(p.lines[i].text)
		switch {
		case body == "":
		case isComment(body):
			p.comment(p.lines[i])
		case body == "---" && len(p.dia.Stmts) == 0:
			i = p.directive(i, KindFrontmatter, "frontmatter", "---")
		case strings.HasPrefix(body, "%%{"):
			i = p.directive(i, KindInit, "init", "}%%")
		default:
			return i
		}
	}
	return -1
}

// directive consumes a multi-line block that ends with close (front matter,
// an init directive) as one statement, and returns the index of its last
// line. An unterminated block claims only its opening line, so the rest of
// the diagram still parses.
func (p *parser) directive(i int, kind, name, closing string) int {
	first := p.lines[i]
	for j := i + 1; j < len(p.lines); j++ {
		if !strings.Contains(p.lines[j].text, closing) {
			continue
		}
		last := p.lines[j]
		text := strings.TrimSpace(strings.Join(lineTexts(p.lines[i:j+1]), "\n"))
		p.add(&Stmt{
			Kind: kind, Name: name, Text: text, Line: first.num,
			Start: first.start + indent(first.text),
			End:   last.start + len(strings.TrimRight(last.text, " \t\r")),
		})
		return j
	}
	body := strings.TrimSpace(first.text)
	p.add(&Stmt{
		Kind: kind, Name: name, Text: body, Line: first.num,
		Start: first.start + indent(first.text),
		End:   first.start + indent(first.text) + len(body),
	})
	return i
}

func lineTexts(lines []srcLine) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, ln.text)
	}
	return out
}

func indent(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

func isComment(body string) bool {
	return strings.HasPrefix(body, "%%") && !strings.HasPrefix(body, "%%{")
}

// comment remembers a `%%` line for the statement below it.
func (p *parser) comment(ln srcLine) {
	body := strings.TrimSpace(ln.text)
	if len(p.pending) == 0 {
		p.pendAt = ln.start + indent(ln.text)
		p.pendLine = ln.num
	}
	p.pendEnd = ln.start + len(strings.TrimRight(ln.text, " \t\r"))
	p.pending = append(p.pending, strings.TrimSpace(strings.TrimPrefix(body, "%%")))
	p.raw = append(p.raw, body)
}

// flush turns comments that never found a statement — a note at the end of a
// subgraph or of the file — into a statement of their own, so the review page
// shows every line the author wrote.
func (p *parser) flush() {
	if len(p.pending) == 0 {
		return
	}
	s := &Stmt{
		Kind: KindComment, Name: "comment", Text: strings.Join(p.raw, " "),
		Line: p.pendLine, Start: p.pendAt, End: p.pendEnd,
	}
	p.pending, p.raw = nil, nil
	p.add(s)
}

// add appends a statement to the innermost open subgraph, giving it any
// comment waiting above it.
func (p *parser) add(s *Stmt) {
	if len(p.pending) > 0 {
		s.Comment = strings.Join(p.pending, " ")
		s.Start = p.pendAt
		p.pending, p.raw = nil, nil
	}
	if n := len(p.stack); n > 0 {
		parent := p.stack[n-1]
		parent.Children = append(parent.Children, s)
		return
	}
	p.dia.Stmts = append(p.dia.Stmts, s)
}

// statement classifies one statement and files it.
func (p *parser) statement(sp span, line int) {
	if sp.text == "" {
		return
	}
	s := &Stmt{Text: sp.text, Line: line, Start: sp.start, End: sp.end}
	kw, rest := keyword(sp.text)
	switch {
	case strings.EqualFold(kw, "subgraph"):
		s.Kind, s.Name = KindSubgraph, or(pathToken(rest), "subgraph")
		p.add(s)
		p.stack = append(p.stack, s)
		return
	case strings.EqualFold(sp.text, "end"):
		// The `end` line closes a subgraph; it says nothing of its own, so it
		// gets no anchor — but a comment stranded above it still needs one.
		p.flush()
		if n := len(p.stack); n > 0 {
			p.stack = p.stack[:n-1]
			return
		}
		s.Kind, s.Name = KindStatement, "end"
	case directives[kw] != "":
		s.Kind = directives[kw]
		s.Name = s.Kind
		if t := pathToken(rest); t != "" {
			s.Name += "/" + t
		}
	default:
		if ops := linkTokens(sp.text); len(ops) > 0 {
			s.Kind = KindEdge
			s.Name = or(strings.Join(edgeNodes(sp.text, ops), "-->"), pathToken(sp.text))
			s.Name = or(s.Name, "edge")
			break
		}
		s.Kind = KindNode
		s.Name = or(pathToken(sp.text), "node")
	}
	p.add(s)
}

func or(s, alt string) string {
	if s != "" {
		return s
	}
	return alt
}

// keyword splits off a statement's first word, which is what decides whether
// it describes the diagram rather than its graph.
func keyword(text string) (kw, rest string) {
	i := strings.IndexAny(text, " \t")
	if i < 0 {
		return text, ""
	}
	return text[:i], strings.TrimSpace(text[i:])
}

// splitStatements cuts a line at its top-level semicolons: mermaid allows
// `A-->B; B-->C`, and each half is its own statement with its own anchor.
func splitStatements(ln srcLine) []span {
	top := topLevel(ln.text)
	var out []span
	at := 0
	for i := 0; i <= len(ln.text); i++ {
		if i < len(ln.text) && !separator(ln.text, top, i) {
			continue
		}
		if sp, ok := trimSpan(ln, at, i); ok {
			out = append(out, sp)
		}
		at = i + 1
	}
	if len(out) == 0 {
		out = append(out, span{start: ln.start, end: ln.start})
	}
	return out
}

// separator reports whether byte i ends a statement: a semicolon that is not
// inside a label or a quote.
func separator(s string, top []bool, i int) bool { return s[i] == ';' && top[i] }

// trimSpan turns a slice of a line into a span over its trimmed text, so an
// anchor never covers the indentation in front of a statement.
func trimSpan(ln srcLine, from, to int) (span, bool) {
	seg := ln.text[from:to]
	text := strings.TrimSpace(seg)
	if text == "" {
		return span{}, false
	}
	start := ln.start + from + indent(seg)
	return span{text: text, start: start, end: start + len(text)}, true
}

// topLevel marks the bytes of a line that sit outside every bracket and
// quote, so a `;` or an arrow inside a node label is left alone.
func topLevel(s string) []bool {
	out := make([]bool, len(s))
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quoted:
			if c == '"' {
				quoted = false
			}
		case c == '"':
			quoted = true
		case c == '[' || c == '(' || c == '{':
			depth++
		case c == ']' || c == ')' || c == '}':
			if depth > 0 {
				depth--
			}
		default:
			out[i] = depth == 0
		}
	}
	return out
}

// linkRe matches the arrow, thick, dotted and invisible link operators of a
// flowchart, in every length and with every head mermaid allows.
var linkRe = regexp.MustCompile(`~{3,}|[ox<]?-{2,}[ox>]?|[ox<]?={2,}[ox>]?|-\.+-*[ox>]?|\.-+[ox>]?`)

// linkTokens finds the link operators of a statement, outside labels and
// quotes. The `o` and `x` heads are only heads when a space sets them off:
// mermaid has the same ambiguity, and `foo-->bar` must not lose the `o` of
// `foo` to the arrow.
func linkTokens(s string) [][2]int {
	top := topLevel(s)
	var out [][2]int
	for _, m := range linkRe.FindAllStringIndex(s, -1) {
		lo, hi := m[0], m[1]
		if !top[lo] {
			continue
		}
		if marker(s[lo]) && lo > 0 && !space(s[lo-1]) {
			lo++
		}
		if marker(s[hi-1]) && hi < len(s) && !space(s[hi]) {
			hi--
		}
		if hi-lo < 2 {
			continue
		}
		out = append(out, [2]int{lo, hi})
	}
	return out
}

func marker(c byte) bool { return c == 'o' || c == 'x' }
func space(c byte) bool  { return c == ' ' || c == '\t' }

// hasTip reports whether a link operator ends an edge. `A -- text --> B`
// labels an edge; `A --> B --> C` chains two. The difference is that the
// first operator of the labelled form has no tip, which is how mermaid itself
// tells them apart.
func hasTip(op string) bool {
	if strings.HasPrefix(op, "~") {
		return true
	}
	last := op[len(op)-1]
	return last == '>' || marker(last)
}

// edgeNodes returns the node ids an edge statement connects, skipping the
// labels between an untipped operator and the one that closes it.
func edgeNodes(s string, ops [][2]int) []string {
	var ids []string
	at := 0
	for i, op := range ops {
		seg := s[at:op[0]]
		label := i > 0 && !hasTip(s[ops[i-1][0]:ops[i-1][1]])
		if id := nodeID(seg); id != "" && !label {
			ids = append(ids, id)
		}
		at = op[1]
	}
	if id := nodeID(s[at:]); id != "" {
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		return nil
	}
	return ids
}

// nodeID is the identifier an edge's endpoint refers to, without the edge
// label that may precede it or the shape and text that follow it.
func nodeID(seg string) string { return pathToken(stripLabel(seg)) }

// stripLabel drops a leading `|edge label|`, which belongs to the arrow
// before it rather than to the node after it.
func stripLabel(seg string) string {
	seg = strings.TrimSpace(seg)
	if !strings.HasPrefix(seg, "|") {
		return seg
	}
	if i := strings.Index(seg[1:], "|"); i >= 0 {
		return strings.TrimSpace(seg[i+2:])
	}
	return strings.TrimSpace(strings.TrimPrefix(seg, "|"))
}

// pathToken reduces a statement or endpoint to the identifier that names it —
// the part before any shape, label, class or quote — which is what a block
// path is built from.
func pathToken(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(" \t[({<>@\"/\\:|", s[i]) >= 0 {
			return s[:i]
		}
	}
	return s
}
