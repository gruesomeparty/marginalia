package codetree

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Java anchors a type by its name, and its members after a slash:
//
//	package com.x;              package
//	class Batch { … }           Batch
//	  private int cap;          Batch/cap
//	  Batch(int c) { … }        Batch/Batch (the constructor)
//	  boolean full() { … }      Batch/full; an overload Batch/full#2
//	  enum Color { … }          Batch/Color
//	record Point(int x) {}      Point
//
// Javadoc and annotations are part of the declaration they precede.
func init() {
	register(&Lang{Name: "java", Extensions: []string{".java"}, Grammar: "java", decl: javaDecl})
}

func javaDecl(w *walker, n *ts.Node) (decl, bool) {
	switch typ := w.typ(n); typ {
	case "package_declaration":
		return decl{name: "package", kind: "package"}, true
	case "import_declaration":
		return decl{name: "import", kind: "import"}, true
	case "class_declaration", "interface_declaration", "record_declaration", "annotation_type_declaration":
		kind := strings.TrimSuffix(typ, "_declaration")
		return decl{name: w.field(n, "name"), kind: kind, members: w.named(n, "class_body", "interface_body", "annotation_type_body")}, true
	case "enum_declaration":
		return decl{name: w.field(n, "name"), kind: "enum"}, true
	case "method_declaration", "annotation_type_element_declaration":
		return decl{name: w.field(n, "name"), kind: "method"}, true
	case "constructor_declaration", "compact_constructor_declaration":
		return decl{name: w.field(n, "name"), kind: "constructor"}, true
	case "field_declaration", "constant_declaration":
		var names []string
		for i := 0; i < n.ChildCount(); i++ {
			if n.FieldNameForChild(i, w.lang) == "declarator" {
				names = append(names, w.field(n.Child(i), "name"))
			}
		}
		return decl{name: strings.Join(names, ","), kind: "field"}, true
	case "static_initializer":
		return decl{name: "static", kind: "static"}, true
	}
	return decl{}, false
}
