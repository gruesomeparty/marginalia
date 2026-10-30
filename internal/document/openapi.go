package document

import "strings"

// httpMethods are the keys of an OpenAPI path item that are operations.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// isOpenAPI reports whether a data document is an OpenAPI (or Swagger) spec:
// one root object carrying an `openapi` or `swagger` key. Detection reads the
// decoded document, never the file name.
func isOpenAPI(roots []dataNode) bool {
	if len(roots) != 1 || roots[0].Kind != kindObject {
		return false
	}
	for _, c := range roots[0].Children {
		if c.Keyed && (c.Key == "openapi" || c.Key == "swagger") {
			return true
		}
	}
	return false
}

// addOpenAPI anchors a spec the way a reviewer names its parts (issue #150):
// an operation by method and path (`GET /orders/{id}`), its path item by the
// path (`/orders/{id}`), a component by kind and name
// (`components/schemas/Order`, Swagger's `definitions/Order`). Below those,
// and everywhere else (`$.info.title`), paths are the generic tree's, so an
// operation's anchor survives every other operation being edited, added or
// removed, and only what the spec gives a name is renamed.
func addOpenAPI(tb *treeBuilder, root dataNode) {
	rootID := tb.add(dataLine(root, "$", "", "", 0))
	for i, top := range root.Children {
		id := tb.add(dataLine(top, dataPath(rootID, top, i), rootID, dataLabel(top, i), 1))
		switch {
		case top.Key == "paths" && top.Kind == kindObject:
			for j, item := range top.Children {
				addPathItem(tb, item, id, j)
			}
		case top.Key == "components" && top.Kind == kindObject:
			for j, group := range top.Children {
				gid := tb.add(dataLine(group, "components/"+group.Key, id, dataLabel(group, j), 2))
				addNamed(tb, group, gid, gid, 3)
			}
		case top.Key == "definitions" && top.Kind == kindObject:
			addNamed(tb, top, "definitions", id, 2)
		default:
			addDataChildren(tb, top, id, 2)
		}
	}
}

// addPathItem adds one entry of `paths`, anchored by its path, and its
// operations, anchored by method and path.
func addPathItem(tb *treeBuilder, item dataNode, parent string, index int) {
	id := tb.add(dataLine(item, item.Key, parent, dataLabel(item, index), 2))
	for k, op := range item.Children {
		if !op.Keyed || !httpMethods[strings.ToLower(op.Key)] {
			addDataNode(tb, op, dataPath(id, op, k), id, dataLabel(op, k), 3)
			continue
		}
		opID := tb.add(dataLine(op, strings.ToUpper(op.Key)+" "+item.Key, id, dataLabel(op, k), 3))
		addDataChildren(tb, op, opID, 4)
	}
}

// addNamed adds a group of named definitions under parent, each anchored as
// prefix plus its name: `components/schemas/Order`, `definitions/Order`.
func addNamed(tb *treeBuilder, group dataNode, prefix, parent string, level int) {
	for k, def := range group.Children {
		id := prefix + "/" + def.Key
		if !def.Keyed {
			id = dataPath(prefix, def, k) // a group written as a list
		}
		did := tb.add(dataLine(def, id, parent, dataLabel(def, k), level))
		addDataChildren(tb, def, did, level+1)
	}
}

func addDataChildren(tb *treeBuilder, n dataNode, id string, level int) {
	for i, child := range n.Children {
		addDataNode(tb, child, dataPath(id, child, i), id, dataLabel(child, i), level)
	}
}
