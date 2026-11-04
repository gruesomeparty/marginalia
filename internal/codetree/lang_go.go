package codetree

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Go anchors a declaration by the name a Go programmer would use for it:
//
//	package ingest              package
//	import (…)                  import
//	func Ingest(…)              Ingest
//	func (b *Batch) Full()      Batch/Full
//	type Batch struct           Batch, a field of it Batch/Cap
//	type Doer interface         Doer, a method of it Doer/Do
//	const batchCap = 10         const/batchCap, the same inside a group
//	var x, y int                var/x,y
//
// A method stays where the file put it, not under its type: the page reads
// in source order, and the path already says whose method it is.
func init() {
	register(&Lang{Name: "go", Extensions: []string{".go"}, Grammar: "go", decl: goDecl})
}

func goDecl(w *walker, n *ts.Node) (decl, bool) {
	switch typ := w.typ(n); typ {
	case "package_clause":
		return decl{name: "package", kind: "package"}, true
	case "import_declaration":
		return decl{name: "import", kind: "import"}, true
	case "function_declaration":
		return decl{name: w.field(n, "name"), kind: "func"}, true
	case "method_declaration":
		return decl{name: join(goReceiver(w, n), w.field(n, "name")), kind: "method"}, true
	case "const_declaration", "var_declaration":
		kw := strings.TrimSuffix(typ, "_declaration")
		specs := w.named(n, kw+"_spec")
		if len(specs) == 1 && !w.has(n, "(") {
			return decl{name: join(kw, goNames(w, specs[0])), kind: kw}, true
		}
		return decl{name: kw, kind: kw, members: []*ts.Node{n}}, true
	case "const_spec", "var_spec":
		return decl{name: goNames(w, n), kind: strings.TrimSuffix(typ, "_spec")}, true
	case "type_declaration":
		specs := w.named(n, "type_spec", "type_alias")
		if len(specs) == 1 && !w.has(n, "(") {
			return goType(w, specs[0]), true
		}
		return decl{name: "type", kind: "type", members: []*ts.Node{n}, flat: true}, true
	case "type_spec", "type_alias":
		return goType(w, n), true
	case "field_declaration":
		if names := w.fields(n, "name"); len(names) > 0 {
			return decl{name: strings.Join(names, ","), kind: "field"}, true
		}
		return decl{name: goEmbedded(w.field(n, "type")), kind: "embed"}, true
	case "method_elem":
		return decl{name: w.field(n, "name"), kind: "method"}, true
	case "type_elem":
		return decl{name: goEmbedded(w.text(n)), kind: "embed"}, true
	}
	return decl{}, false
}

// goType reads one type spec: a struct's fields and an interface's methods
// are its members, anything else is a leaf.
func goType(w *walker, spec *ts.Node) decl {
	d := decl{name: w.field(spec, "name"), kind: "type"}
	t := spec.ChildByFieldName("type", w.lang)
	if t == nil || w.typ(spec) == "type_alias" {
		return d
	}
	switch w.typ(t) {
	case "struct_type":
		d.kind = "struct"
		d.members = w.named(t, "field_declaration_list")
	case "interface_type":
		d.kind = "interface"
		d.members = []*ts.Node{t}
	}
	if d.members == nil && d.kind != "type" {
		d.members = []*ts.Node{} // an empty struct is still a container
	}
	return d
}

// goReceiver is the type a method belongs to, without the pointer or type
// parameters: `(b *Batch[T])` is `Batch`.
func goReceiver(w *walker, n *ts.Node) string {
	recv := n.ChildByFieldName("receiver", w.lang)
	if recv == nil {
		return ""
	}
	for _, p := range w.named(recv, "parameter_declaration") {
		t := p.ChildByFieldName("type", w.lang)
		for t != nil {
			switch w.typ(t) {
			case "pointer_type", "parenthesized_type":
				t = firstNamed(t)
				continue
			case "generic_type":
				t = t.ChildByFieldName("type", w.lang)
				continue
			}
			return w.text(t)
		}
	}
	return ""
}

// goNames is a spec's declared names, comma-joined: `var x, y int` is `x,y`.
func goNames(w *walker, n *ts.Node) string {
	return strings.Join(w.fields(n, "name"), ",")
}

// goEmbedded names an embedded field by its type, less any pointer.
func goEmbedded(t string) string { return strings.TrimLeft(strings.TrimSpace(t), "*") }

func firstNamed(n *ts.Node) *ts.Node {
	if n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(0)
}
