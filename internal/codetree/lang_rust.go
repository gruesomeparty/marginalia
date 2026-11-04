package codetree

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Rust anchors an item by its name, and what it holds after a slash:
//
//	const CAP: usize            CAP
//	struct Batch { cap: usize } Batch, Batch/cap
//	enum Color { Red }          Color, Color/Red
//	trait Doer { fn run(); }    Doer, Doer/run
//	impl Batch { fn full() }    impl Batch, its method Batch/full
//	impl Display for Batch      impl Display for Batch, Batch/fmt
//	mod inner { fn f() }        inner, inner/f
//
// An impl block's methods hang under the type they belong to, so a method
// reads the same whichever impl declares it. `#[…]` attributes are part of
// the item below them.
func init() {
	register(&Lang{Name: "rust", Extensions: []string{".rs"}, Grammar: "rust", decl: rustDecl, attached: []string{"attribute_item"}})
}

func rustDecl(w *walker, n *ts.Node) (decl, bool) {
	switch typ := w.typ(n); typ {
	case "function_item", "function_signature_item":
		kind := "fn"
		if w.in == "impl" || w.in == "trait" {
			kind = "method"
		}
		return decl{name: w.field(n, "name"), kind: kind}, true
	case "struct_item", "union_item":
		return decl{name: w.field(n, "name"), kind: strings.TrimSuffix(typ, "_item"), members: w.named(n, "field_declaration_list")}, true
	case "enum_item":
		return decl{name: w.field(n, "name"), kind: "enum", members: w.named(n, "enum_variant_list")}, true
	case "trait_item":
		return decl{name: w.field(n, "name"), kind: "trait", members: w.named(n, "declaration_list")}, true
	case "mod_item":
		d := decl{name: w.field(n, "name"), kind: "mod"}
		if body := w.named(n, "declaration_list"); len(body) > 0 {
			d.members = body
		}
		return d, true
	case "impl_item":
		self := rustTypeName(w, n.ChildByFieldName("type", w.lang))
		name, scope := "impl "+self, self
		// A trait's methods sit under the type and the trait, so that
		// `fmt` from Display and from Debug keep their own ids whichever
		// impl comes first.
		if trait := n.ChildByFieldName("trait", w.lang); trait != nil {
			t := rustTypeName(w, trait)
			name, scope = "impl "+t+" for "+self, self+"/"+t
		}
		return decl{name: name, kind: "impl", scope: scope, members: w.named(n, "declaration_list")}, true
	case "const_item", "static_item", "type_item", "macro_definition", "associated_type":
		return decl{name: w.field(n, "name"), kind: strings.TrimSuffix(strings.TrimSuffix(typ, "_item"), "_definition")}, true
	case "field_declaration", "enum_variant":
		return decl{name: w.field(n, "name"), kind: map[string]string{"field_declaration": "field", "enum_variant": "variant"}[typ]}, true
	}
	return decl{}, false
}

// rustTypeName is a type without its generics or references: `Batch<T>` and
// `&mut Batch` are `Batch`, `fmt::Display` is `Display`.
func rustTypeName(w *walker, t *ts.Node) string {
	for t != nil {
		switch w.typ(t) {
		case "generic_type", "reference_type", "pointer_type":
			t = t.ChildByFieldName("type", w.lang)
		case "scoped_type_identifier", "scoped_identifier":
			t = t.ChildByFieldName("name", w.lang)
		default:
			return w.text(t)
		}
	}
	return ""
}
