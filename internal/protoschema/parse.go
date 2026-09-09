// Package protoschema parses .proto source into a declaration tree.
//
// The parser is deliberately structural rather than semantic: it does not
// resolve imports, check types, or reject unknown constructs. Marginalia only
// needs to know which declarations exist, how they nest, and what each one
// says verbatim — so a file protoc would refuse (a stray keyword, an import
// that isn't on disk) still renders as a reviewable tree.
package protoschema

import (
	"errors"
	"strings"
)

// Declaration kinds produced by Parse.
const (
	KindSyntax     = "syntax"
	KindPackage    = "package"
	KindImport     = "import"
	KindOption     = "option"
	KindMessage    = "message"
	KindField      = "field"
	KindOneof      = "oneof"
	KindGroup      = "group"
	KindEnum       = "enum"
	KindEnumValue  = "enum_value"
	KindService    = "service"
	KindRPC        = "rpc"
	KindReserved   = "reserved"
	KindExtensions = "extensions"
	KindExtend     = "extend"
	KindUnknown    = "unknown"
)

// Decl is one proto declaration. Text is the declaration's own source — for
// block declarations the header only (`message Order`), not the body — so a
// change inside a message does not disturb the message's own anchor.
type Decl struct {
	Kind     string
	Name     string
	Text     string
	Comment  string // comment on the line(s) above the declaration
	Trailing string // comment after the declaration, on its own line
	Line     int
	Children []*Decl
}

// File is a parsed .proto source file.
type File struct {
	Syntax  string
	Package string
	Decls   []*Decl
}

// Scopes a declaration can appear in; classification depends on the scope.
const (
	scopeFile    = "file"
	scopeMessage = "message"
	scopeEnum    = "enum"
	scopeService = "service"
	scopeOneof   = "oneof"
)

// Parse parses proto source into a File. It fails only on input that carries
// no declarations at all, which is never a document worth reviewing.
func Parse(src []byte) (*File, error) {
	p := &parser{src: src, toks: lex(src)}
	f := &File{Decls: p.body(scopeFile)}
	if len(f.Decls) == 0 {
		return nil, errors.New("no proto declarations found")
	}
	for _, d := range f.Decls {
		switch d.Kind {
		case KindSyntax:
			f.Syntax = d.Name
		case KindPackage:
			f.Package = d.Name
		}
	}
	return f, nil
}

type parser struct {
	src  []byte
	toks []token
	i    int
}

func (p *parser) at(k int) (token, bool) {
	if p.i+k >= len(p.toks) {
		return token{}, false
	}
	return p.toks[p.i+k], true
}

func (p *parser) punct(k int, s string) bool {
	t, ok := p.at(k)
	return ok && t.kind == tokPunct && t.text == s
}

// body reads declarations until the closing brace of the enclosing block (or
// end of input) and returns them. The closing brace is consumed.
func (p *parser) body(scope string) []*Decl {
	var decls []*Decl
	for p.i < len(p.toks) {
		if p.punct(0, "}") {
			p.i++
			if scope == scopeFile {
				continue // stray closing brace: nothing to close, keep reading
			}
			return decls
		}
		if p.punct(0, ";") { // stray empty statement
			p.i++
			continue
		}
		if d := p.declaration(scope); d != nil {
			decls = append(decls, d)
		}
	}
	return decls
}

// declaration reads one statement — up to its terminating `;`, or its `{` and
// then the nested body — and classifies it for the given scope.
func (p *parser) declaration(scope string) *Decl {
	first, ok := p.at(0)
	if !ok {
		return nil
	}
	words, block, end, trail := p.statement()
	if len(words) == 0 {
		return nil
	}
	kind, name := classify(words, scope)
	d := &Decl{
		Kind:     kind,
		Name:     name,
		Text:     normalizeSpace(string(p.src[first.start:end])),
		Comment:  cleanComment(first.lead),
		Trailing: cleanComment(trail),
		Line:     first.line,
	}
	if block {
		d.Children = p.body(childScope(kind, scope))
	}
	return d
}

// statement consumes tokens up to a terminating `;` at depth zero, or up to a
// `{` that opens a nested body. It reports the significant words, whether a
// body follows, the end offset of the declaration's own text, and any trailing
// comment on the terminator's line.
func (p *parser) statement() (words []token, block bool, end int, trail string) {
	var parens, brackets, angles int
	optionValue := false
	if t, ok := p.at(0); ok && t.kind == tokIdent && t.text == KindOption {
		optionValue = true // `option (x) = { ... };` keeps its braces inline
	}
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		if t.kind == tokPunct {
			switch t.text {
			case "(":
				parens++
			case ")":
				parens--
			case "[":
				brackets++
			case "]":
				brackets--
			case "<":
				angles++
			case ">":
				angles--
			case ";":
				p.i++
				return words, false, t.end, t.trail
			case "{":
				if parens > 0 || brackets > 0 || angles > 0 || optionValue {
					p.i++
					p.skipBraces()
					continue
				}
				p.i++
				return words, true, t.start, t.trail
			case "}":
				// Unbalanced brace: treat what we have as a declaration and
				// let the caller close the enclosing block.
				return words, false, t.start, ""
			}
		}
		words = append(words, t)
		p.i++
	}
	if len(words) > 0 {
		return words, false, words[len(words)-1].end, ""
	}
	return words, false, 0, ""
}

// skipBraces consumes a balanced brace group whose opening brace was already
// read (an aggregate option value, not a nested declaration scope).
func (p *parser) skipBraces() {
	depth := 1
	for p.i < len(p.toks) && depth > 0 {
		if p.toks[p.i].kind == tokPunct {
			switch p.toks[p.i].text {
			case "{":
				depth++
			case "}":
				depth--
			}
		}
		p.i++
	}
}

func childScope(kind, scope string) string {
	switch kind {
	case KindMessage, KindGroup, KindExtend:
		return scopeMessage
	case KindEnum:
		return scopeEnum
	case KindService:
		return scopeService
	case KindOneof:
		return scopeOneof
	}
	return scope
}

// classify names a declaration from its leading words and the scope it sits in.
func classify(words []token, scope string) (kind, name string) {
	head := words[0].text
	if words[0].kind != tokIdent {
		return KindUnknown, ""
	}
	switch head {
	case KindSyntax:
		return head, literal(joinWords(words[1:]))
	case KindPackage:
		return head, joinWords(words[1:])
	case KindImport:
		return head, literal(joinWords(words[1:]))
	case KindOption:
		return head, wordsBeforeAssign(words[1:])
	case KindReserved, KindExtensions:
		return head, ""
	case KindMessage, KindEnum, KindService, KindOneof, KindExtend, KindGroup:
		return head, wordAfter(words, 0)
	case KindRPC:
		if scope == scopeService {
			return KindRPC, wordAfter(words, 0)
		}
	}
	switch scope {
	case scopeEnum:
		if n := nameBeforeAssign(words); n != "" {
			return KindEnumValue, n
		}
	case scopeMessage, scopeOneof:
		if n := nameBeforeAssign(words); n != "" {
			return KindField, n
		}
	}
	return KindUnknown, ""
}

// joinWords renders a token run back to source-like text.
func joinWords(words []token) string {
	parts := make([]string, 0, len(words))
	for _, w := range words {
		parts = append(parts, w.text)
	}
	return normalizeSpace(strings.Join(parts, " "))
}

// wordsBeforeAssign returns the text left of the first `=`, which for an
// option statement is the option's (possibly parenthesized) name.
func wordsBeforeAssign(words []token) string {
	for i, w := range words {
		if w.kind == tokPunct && w.text == "=" {
			return strings.ReplaceAll(joinWords(words[:i]), " ", "")
		}
	}
	return joinWords(words)
}

// wordAfter returns the identifier following position i, skipping punctuation.
func wordAfter(words []token, i int) string {
	for j := i + 1; j < len(words); j++ {
		if words[j].kind == tokIdent {
			return words[j].text
		}
	}
	return ""
}

// nameBeforeAssign returns the identifier directly before the first `=`,
// which is the field or enum-value name in every proto declaration form
// (`repeated Foo bar = 1`, `map<string, T> m = 2`, `STATUS_OK = 0`).
func nameBeforeAssign(words []token) string {
	for i, w := range words {
		if w.kind == tokPunct && w.text == "=" && i > 0 && words[i-1].kind == tokIdent {
			return words[i-1].text
		}
	}
	return ""
}

// literal returns the contents of the first quoted string in s, or s itself.
func literal(s string) string {
	for _, q := range []string{`"`, `'`} {
		if a := strings.Index(s, q); a >= 0 {
			if b := strings.Index(s[a+1:], q); b >= 0 {
				return s[a+1 : a+1+b]
			}
		}
	}
	return s
}

func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// cleanComment strips comment markers and leading asterisks, leaving prose.
func cleanComment(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "/**")
		line = strings.TrimPrefix(line, "/*")
		line = strings.TrimSuffix(line, "*/")
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimPrefix(line, "*")
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return normalizeSpace(strings.Join(out, " "))
}
