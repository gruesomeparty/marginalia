package codetree

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// TypeScript and JavaScript anchor a declaration by its name, and what a
// class, interface or namespace holds after a slash:
//
//	export const batchCap = 10  batchCap
//	const handler = () => {}    handler
//	function ingest(b) {}       ingest
//	class Batch { full() {} }   Batch, Batch/full, a field Batch/cap
//	interface Doer { do(): v }  Doer, Doer/do
//	type Id = string            Id
//	namespace NS { … }          NS, NS/f
//
// `export` and `declare` are part of what they introduce; an anonymous
// default export is `default`.
func init() {
	register(&Lang{Name: "typescript", Extensions: []string{".ts", ".mts", ".cts"}, Grammar: "typescript", decl: tsDecl})
	register(&Lang{Name: "tsx", Extensions: []string{".tsx"}, Grammar: "tsx", decl: tsDecl})
	register(&Lang{Name: "javascript", Extensions: []string{".js", ".mjs", ".cjs", ".jsx"}, Grammar: "javascript", decl: tsDecl})
}

func tsDecl(w *walker, n *ts.Node) (decl, bool) {
	switch typ := w.typ(n); typ {
	case "export_statement":
		if d := n.ChildByFieldName("declaration", w.lang); d != nil {
			return tsDecl(w, d)
		}
		if v := n.ChildByFieldName("value", w.lang); v != nil {
			if d, ok := tsDecl(w, v); ok && d.name != "" {
				return d, true
			}
			return decl{name: "default", kind: "export"}, true
		}
	case "ambient_declaration", "expression_statement":
		if c := firstNamed(n); c != nil {
			return tsDecl(w, c)
		}
	case "lexical_declaration", "variable_declaration":
		var names []string
		for _, v := range w.named(n, "variable_declarator") {
			// A destructuring pattern is its own text, less the layout.
			names = append(names, strings.Join(strings.Fields(w.field(v, "name")), ""))
		}
		kind := "var"
		if n.ChildCount() > 0 {
			kind = w.typ(n.Child(0)) // const, let or var
		}
		return decl{name: strings.Join(names, ","), kind: kind}, true
	case "function_declaration", "generator_function_declaration", "function_signature", "function_expression", "function":
		return decl{name: w.field(n, "name"), kind: "function"}, true
	case "class_declaration", "abstract_class_declaration", "class":
		return decl{name: w.field(n, "name"), kind: "class", members: w.named(n, "class_body")}, true
	case "interface_declaration":
		return decl{name: w.field(n, "name"), kind: "interface", members: w.named(n, "interface_body", "object_type")}, true
	case "internal_module", "module":
		// `declare module "a/b"` keeps its quotes: unquoted, its slash
		// would read as namespace a's member b.
		return decl{name: w.field(n, "name"), kind: "namespace", members: w.named(n, "statement_block")}, true
	case "type_alias_declaration":
		return decl{name: w.field(n, "name"), kind: "type"}, true
	case "enum_declaration":
		return decl{name: w.field(n, "name"), kind: "enum"}, true
	case "method_definition", "method_signature", "abstract_method_signature":
		return decl{name: w.field(n, "name"), kind: "method"}, true
	case "public_field_definition", "field_definition", "property_signature":
		name := w.field(n, "name")
		if name == "" {
			name = w.field(n, "property")
		}
		return decl{name: name, kind: "field"}, true
	case "class_static_block":
		return decl{name: "static", kind: "static"}, true
	}
	return decl{}, false
}
