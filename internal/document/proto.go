package document

import (
	"strings"

	"github.com/gruesomeparty/marginalia/internal/protoschema"
)

// parseProto turns a .proto file into blocks anchored by schema path rather
// than by line: `CreateOrderRequest/customer_id`, `OrderService/CreateOrder`,
// `Status/STATUS_UNSPECIFIED`, nested types dotted
// (`CreateOrderRequest.Line/sku`). A field keeps its own anchor when the file
// is reformatted or when unrelated declarations move around it.
func parseProto(path string, src []byte) (*Document, error) {
	file, err := protoschema.Parse(src)
	if err != nil {
		return nil, err
	}
	tb := newTreeBuilder()
	addProtoDecls(tb, file.Decls, "", "", 0)
	return &Document{Path: path, Format: FormatProto, Blocks: tb.blocks}, nil
}

// addProtoDecls walks declarations in source order. typePath is the dotted
// path of the enclosing type (empty at file scope); scopePath is the ID of the
// declaration that owns these members, which is what member paths hang off.
func addProtoDecls(tb *treeBuilder, decls []*protoschema.Decl, typePath, scopePath string, level int) {
	for _, d := range decls {
		id := tb.add(node{
			Parent: scopePath,
			ID:     protoPath(d, typePath, scopePath),
			Kind:   d.Kind,
			Level:  level,
			Text:   strings.Join(nonEmpty(d.Comment, d.Text, d.Trailing), " "),
			HTML:   treeLine(d.Comment, esc(d.Text), d.Trailing, "decl"),
		})
		if len(d.Children) == 0 {
			continue
		}
		childType := typePath
		if isProtoType(d.Kind) {
			childType = id
		}
		addProtoDecls(tb, d.Children, childType, id, level+1)
	}
}

// isProtoType reports whether a declaration introduces a type whose name
// extends the dotted path its nested types are named under.
func isProtoType(kind string) bool {
	switch kind {
	case protoschema.KindMessage, protoschema.KindEnum, protoschema.KindService,
		protoschema.KindGroup, protoschema.KindExtend:
		return true
	}
	return false
}

// isProtoMember reports whether a declaration is a member of the type or
// service that encloses it, and so hangs off its owner's path.
func isProtoMember(kind string) bool {
	switch kind {
	case protoschema.KindField, protoschema.KindEnumValue,
		protoschema.KindRPC, protoschema.KindOneof:
		return true
	}
	return false
}

// protoPath derives a declaration's block ID: types join their parent type
// with a dot (`CreateOrderRequest.Line`), members hang off their owner with a
// slash (`CreateOrderRequest.Line/sku`), and everything else — syntax,
// package, imports, options, reserved and extension ranges — is keyed by kind
// plus its own name where it has one (`option/go_package`). treeBuilder makes
// any remaining collision unique.
func protoPath(d *protoschema.Decl, typePath, scopePath string) string {
	switch {
	case isProtoType(d.Kind) && d.Name != "":
		return join(typePath, ".", d.Name)
	case isProtoMember(d.Kind) && d.Name != "":
		return join(scopePath, "/", d.Name)
	}
	id := join(scopePath, "/", d.Kind)
	if d.Name != "" {
		id += "/" + d.Name
	}
	return id
}
