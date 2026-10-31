// Package codetree reads a source file into the declarations a reviewer
// points at — a function, a type, a field of it, a constant in a group —
// each with the bytes it occupies and an anchor path built from the names
// the code already spells (`Batch/Full`, `const/batchCap`), the way
// protoschema reads a schema and hcl a Terraform file.
//
// Parsing is tree-sitter, through gotreesitter: a pure-Go runtime, so the
// single binary still builds with CGO_ENABLED=0 for every target. One
// runtime reads every language; what differs per language is only which
// nodes are declarations and what they are called (lang_*.go). Releases
// embed just the grammars named here (the grammar_subset build tags in
// .goreleaser.yaml); a plain `go build` embeds all of them and works the
// same, only bigger.
//
// Like the other structural readers it never refuses a file: tree-sitter
// recovers from syntax errors, what it could not place becomes an "unknown"
// symbol holding those bytes, and no byte outside whitespace goes unshown.
package codetree

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Symbol kinds every language shares. Declarations carry their language's
// own word (func, struct, class, …).
const (
	KindComment = "comment" // a comment that documents nothing after it
	KindCode    = "code"    // a statement that declares nothing
	KindUnknown = "unknown" // bytes the parser could not place
)

// Symbol is one reviewable declaration.
type Symbol struct {
	Path     string // anchor: `Batch/Full`, `const/batchCap`, `Doer/Do`
	Kind     string
	Start    int // first byte, including the comment documenting it
	End      int // one past the last byte, including a trailing comment
	Children []*Symbol
}

// Head is the end of what a symbol says itself: everything before its
// first child, or the whole symbol when it has none.
func (s *Symbol) Head() int {
	if len(s.Children) > 0 {
		return s.Children[0].Start
	}
	return s.End
}

// Tail is the start of what a container says after its last child — the
// closing brace, usually — or End when it has none.
func (s *Symbol) Tail() int {
	if len(s.Children) > 0 {
		return s.Children[len(s.Children)-1].End
	}
	return s.End
}

// Span marks bytes the page colours: a comment, string, number or keyword.
type Span struct {
	Start, End int
	Class      string
}

// File is a parsed source file.
type File struct {
	Lang    string
	Symbols []*Symbol
	Spans   []Span // sorted, non-overlapping
}

// Lang is one supported language.
type Lang struct {
	Name       string   // "go"
	Extensions []string // ".go"
	// Grammar names the gotreesitter grammar, and with it the
	// grammar_subset_<Grammar> build tag a release must carry.
	Grammar string
	decl    func(w *walker, n *ts.Node) (decl, bool)
}

// decl is what a language says about one node.
type decl struct {
	name string // its own segment of the path; may itself contain '/'
	kind string
	// members are the nodes whose named children are this declaration's
	// own declarations; nil makes it a leaf, read whole.
	members []*ts.Node
	// flat gives members the enclosing scope rather than this path, for a
	// grouping keyword that names nothing (Go's `type ( … )`).
	flat bool
}

var langs []*Lang

func register(l *Lang) { langs = append(langs, l) }

// Detect returns the language path is written in, or nil.
func Detect(path string) *Lang {
	ext := strings.ToLower(filepath.Ext(path))
	for _, l := range langs {
		for _, e := range l.Extensions {
			if e == ext {
				return l
			}
		}
	}
	return nil
}

// Languages lists every supported language, by name.
func Languages() []*Lang {
	out := append([]*Lang(nil), langs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// timeout bounds one parse. tree-sitter is linear in practice, but an
// agent's input reaches this in hosted mode, so a pathological file is
// refused rather than allowed to hold a request.
const timeout = 10_000_000 // microseconds

// Parse reads src as l. An error means no tree came back at all (the
// grammar did not load, the parse timed out or panicked); the caller then
// shows the file whole. A file with syntax errors is not an error.
func Parse(l *Lang, src []byte) (f *File, err error) {
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("parse %s: %v", l.Name, r)
		}
	}()
	entry := grammars.DetectLanguageByName(l.Grammar)
	if entry == nil {
		return nil, fmt.Errorf("grammar %s is not in this build", l.Grammar)
	}
	lang := entry.Language()
	if lang == nil {
		return nil, fmt.Errorf("grammar %s did not load", l.Grammar)
	}
	p := ts.NewParser(lang)
	p.SetTimeoutMicros(timeout)
	var tree *ts.Tree
	if entry.TokenSourceFactory != nil {
		tree, err = p.ParseWithTokenSource(src, entry.TokenSourceFactory(src, lang))
	} else {
		tree, err = p.Parse(src)
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", l.Name, err)
	}
	if tree == nil || tree.RootNode() == nil {
		return nil, errors.New("parse " + l.Name + ": no tree")
	}
	defer tree.Release()
	w := &walker{lang: lang, src: src, l: l}
	root := tree.RootNode()
	f = &File{Lang: l.Name, Symbols: w.cover(w.members([]*ts.Node{root}, ""), 0, len(src))}
	w.colour(root)
	f.Spans = w.spans
	return f, nil
}

// walker carries one parse.
type walker struct {
	lang  *ts.Language
	src   []byte
	l     *Lang
	spans []Span
}

func (w *walker) typ(n *ts.Node) string  { return n.Type(w.lang) }
func (w *walker) text(n *ts.Node) string { return string(w.src[n.StartByte():n.EndByte()]) }

// field is the text of n's child in field name, or "".
func (w *walker) field(n *ts.Node, name string) string {
	if c := n.ChildByFieldName(name, w.lang); c != nil {
		return w.text(c)
	}
	return ""
}

// fields is the text of every child of n in field name, in order.
func (w *walker) fields(n *ts.Node, name string) []string {
	var out []string
	for i := 0; i < n.ChildCount(); i++ {
		if n.FieldNameForChild(i, w.lang) == name {
			out = append(out, w.text(n.Child(i)))
		}
	}
	return out
}

// named returns n's named children, optionally only those of the given types.
func (w *walker) named(n *ts.Node, types ...string) []*ts.Node {
	var out []*ts.Node
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if len(types) == 0 || contains(types, w.typ(c)) {
			out = append(out, c)
		}
	}
	return out
}

// has reports whether n has an anonymous child spelled tok — Go's `(` that
// makes a declaration a group.
func (w *walker) has(n *ts.Node, tok string) bool {
	for i := 0; i < n.ChildCount(); i++ {
		if c := n.Child(i); !c.IsNamed() && w.typ(c) == tok {
			return true
		}
	}
	return false
}

func (w *walker) isComment(n *ts.Node) bool { return strings.Contains(w.typ(n), "comment") }

// members turns the named children of lists into symbols under scope. A
// comment run directly above a declaration (no blank line between) is that
// declaration's documentation and part of its range; a comment on the same
// line as the end of the previous symbol trails it; any other comment run is
// a symbol of its own.
func (w *walker) members(lists []*ts.Node, scope string) []*Symbol {
	var out []*Symbol
	var run []*ts.Node // comments not yet given to a symbol
	lastWasSymbol := false
	flush := func() {
		if len(run) == 0 {
			return
		}
		out = append(out, &Symbol{
			Path: join(scope, KindComment), Kind: KindComment,
			Start: int(run[0].StartByte()), End: int(run[len(run)-1].EndByte()),
		})
		run = nil
	}
	for _, list := range lists {
		for _, n := range w.named(list) {
			if w.isComment(n) {
				switch {
				case len(run) == 0 && lastWasSymbol && !w.newlineBetween(out[len(out)-1].End, int(n.StartByte())):
					out[len(out)-1].End = int(n.EndByte())
				case len(run) > 0 && w.blankBetween(int(run[len(run)-1].EndByte()), int(n.StartByte())):
					flush()
					run = append(run, n)
				default:
					run = append(run, n)
				}
				lastWasSymbol = false
				continue
			}
			s := w.symbol(n, scope)
			if len(run) > 0 {
				if w.blankBetween(int(run[len(run)-1].EndByte()), s.Start) {
					flush()
				} else {
					s.Start = int(run[0].StartByte())
					run = nil
				}
			}
			out = append(out, s)
			lastWasSymbol = true
		}
	}
	flush()
	return out
}

// symbol reads one node: a declaration if the language says so, otherwise a
// statement named by its node type, or an unknown for a syntax error.
func (w *walker) symbol(n *ts.Node, scope string) *Symbol {
	d, ok := w.l.decl(w, n)
	switch {
	case n.IsError():
		d = decl{name: "error", kind: KindUnknown}
	case !ok || d.name == "":
		d = decl{name: stmtName(w.typ(n)), kind: KindCode}
	}
	s := &Symbol{Path: join(scope, d.name), Kind: d.kind, Start: int(n.StartByte()), End: int(n.EndByte())}
	if d.members != nil {
		inner := s.Path
		if d.flat {
			inner = scope
		}
		s.Children = w.members(d.members, inner)
	}
	return s
}

// cover adds an unknown symbol for any run of non-whitespace bytes between
// the file's top-level symbols that none of them holds — a stray token, or
// the rest of a file after a parse that stopped early — so nothing in the
// file goes unshown. Inside a container the gaps are the container's own
// text, so this runs at the top level only.
func (w *walker) cover(syms []*Symbol, from, to int) []*Symbol {
	var out []*Symbol
	at := from
	gap := func(end int) {
		lo, hi := at, end
		for lo < hi && isSpace(w.src[lo]) {
			lo++
		}
		for hi > lo && isSpace(w.src[hi-1]) {
			hi--
		}
		if lo < hi {
			out = append(out, &Symbol{Path: KindUnknown, Kind: KindUnknown, Start: lo, End: hi})
		}
	}
	for _, s := range syms {
		if s.Start > at {
			gap(s.Start)
		}
		out = append(out, s)
		at = max(at, s.End)
	}
	gap(to)
	return out
}

func (w *walker) newlineBetween(a, b int) bool {
	return a <= b && b <= len(w.src) && strings.Contains(string(w.src[a:b]), "\n")
}

func (w *walker) blankBetween(a, b int) bool {
	return a <= b && b <= len(w.src) && strings.Count(string(w.src[a:b]), "\n") > 1
}

// stmtName names a node that declares nothing by what it is: Python's
// `import_statement` is `import`, Go's `package_clause` is `package`.
func stmtName(typ string) string {
	for _, suffix := range []string{"_statement", "_declaration", "_definition", "_clause", "_expression"} {
		if t := strings.TrimSuffix(typ, suffix); t != typ && t != "" {
			return t
		}
	}
	return typ
}

func join(scope, name string) string {
	if scope == "" {
		return name
	}
	return scope + "/" + name
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
