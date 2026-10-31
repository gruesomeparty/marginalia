package codetree

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Python anchors a class, function or module-level name by its name, and
// what a class holds after a slash:
//
//	RETRIES = 3                 RETRIES
//	class Batch:                Batch
//	    cap: int = 0            Batch/cap
//	    def full(self):         Batch/full
//	async def ingest(b):        ingest
//
// Decorators are part of what they decorate. A docstring stays with the
// class or function it documents, and a function's nested functions are part
// of its body, read whole like any other leaf.
func init() {
	register(&Lang{Name: "python", Extensions: []string{".py", ".pyi"}, Grammar: "python", decl: pyDecl})
}

func pyDecl(w *walker, n *ts.Node) (decl, bool) {
	switch w.typ(n) {
	case "decorated_definition":
		if def := n.ChildByFieldName("definition", w.lang); def != nil {
			return pyDecl(w, def)
		}
	case "class_definition":
		return decl{name: w.field(n, "name"), kind: "class", members: w.named(n, "block")}, true
	case "function_definition":
		kind := "def"
		if w.in == "class" {
			kind = "method"
		}
		return decl{name: w.field(n, "name"), kind: kind}, true
	case "expression_statement":
		if c := firstNamed(n); c != nil {
			return pyDecl(w, c)
		}
	case "assignment", "augmented_assignment":
		if left := n.ChildByFieldName("left", w.lang); left != nil && pyName(w.typ(left)) {
			return decl{name: strings.Join(strings.Fields(w.text(left)), ""), kind: "assign"}, true
		}
	case "import_statement", "import_from_statement", "future_import_statement":
		return decl{name: "import", kind: "import"}, true
	case "string":
		// A docstring: part of the class or function it documents, or the
		// module's own block at the top.
		if w.in != "" {
			return decl{skip: true}, true
		}
		return decl{name: "docstring", kind: "doc"}, true
	}
	return decl{}, false
}

// pyName reports whether an assignment target names something: `x`, `x, y`,
// `self.x` — not a subscript.
func pyName(typ string) bool {
	return typ == "identifier" || typ == "pattern_list" || typ == "attribute"
}
