package document

import (
	"strings"
	"testing"
)

const protoSrc = `syntax = "proto3";

package orders.v1;

import "google/protobuf/timestamp.proto";

// An order the customer wants placed.
message CreateOrderRequest {
  // Stable customer id.
  string customer_id = 1;
  reserved 4, 7 to 9;

  message Line {
    string sku = 1;
  }

  oneof payment {
    string invoice_ref = 6;
  }
}

enum Status {
  STATUS_UNSPECIFIED = 0;
}

service OrderService {
  rpc CreateOrder(CreateOrderRequest) returns (Status);
}
`

func blockByID(doc *Document, id string) *Block {
	for i := range doc.Blocks {
		if doc.Blocks[i].ID == id {
			return &doc.Blocks[i]
		}
	}
	return nil
}

func parseProtoDoc(t *testing.T) *Document {
	t.Helper()
	doc, err := ParseBytes("api.proto", []byte(protoSrc))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if doc.Format != FormatProto || !doc.IsTree() {
		t.Fatalf("format = %q, IsTree = %v", doc.Format, doc.IsTree())
	}
	return doc
}

// The acceptance criterion of issue #15: blocks are anchored by dotted schema
// path, not by line number.
func TestProtoBlockPaths(t *testing.T) {
	doc := parseProtoDoc(t)
	want := []struct {
		id, kind, parent string
		level            int
	}{
		{"syntax/proto3", "syntax", "", 0},
		{"package/orders.v1", "package", "", 0},
		{"import/google/protobuf/timestamp.proto", "import", "", 0},
		{"CreateOrderRequest", "message", "", 0},
		{"CreateOrderRequest/customer_id", "field", "CreateOrderRequest", 1},
		{"CreateOrderRequest/reserved", "reserved", "CreateOrderRequest", 1},
		{"CreateOrderRequest.Line", "message", "CreateOrderRequest", 1},
		{"CreateOrderRequest.Line/sku", "field", "CreateOrderRequest.Line", 2},
		{"CreateOrderRequest/payment", "oneof", "CreateOrderRequest", 1},
		{"CreateOrderRequest/payment/invoice_ref", "field", "CreateOrderRequest/payment", 2},
		{"Status", "enum", "", 0},
		{"Status/STATUS_UNSPECIFIED", "enum_value", "Status", 1},
		{"OrderService", "service", "", 0},
		{"OrderService/CreateOrder", "rpc", "OrderService", 1},
	}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d", len(doc.Blocks), len(want))
	}
	for i, w := range want {
		got := doc.Blocks[i]
		if got.ID != w.id || got.Kind != w.kind || got.Parent != w.parent || got.Level != w.level {
			t.Errorf("block %d = {%s %s parent=%s level=%d}, want {%s %s parent=%s level=%d}",
				i, got.ID, got.Kind, got.Parent, got.Level, w.id, w.kind, w.parent, w.level)
		}
	}
}

func TestProtoBlockAnchorFields(t *testing.T) {
	doc := parseProtoDoc(t)
	f := blockByID(doc, "CreateOrderRequest/customer_id")
	if f == nil {
		t.Fatal("field block missing")
	}
	// The leading comment plus the declaration is the quote, per issue #15.
	if f.Quote != "Stable customer id. string customer_id = 1;" {
		t.Errorf("quote = %q", f.Quote)
	}
	if len(f.Hash) != 12 {
		t.Errorf("hash = %q, want 12 hex chars", f.Hash)
	}
	if !strings.Contains(f.HTML, "string customer_id = 1;") || !strings.Contains(f.HTML, `class="cmt"`) {
		t.Errorf("HTML = %q", f.HTML)
	}
	if f.HasChildren {
		t.Error("a scalar field should have no children")
	}
	if !blockByID(doc, "CreateOrderRequest").HasChildren {
		t.Error("message block should be marked as having children")
	}
}

// A field's anchor must survive edits elsewhere in the file: that is the whole
// point of pathing by schema instead of by position.
func TestProtoHashStableAcrossUnrelatedEdits(t *testing.T) {
	before := parseProtoDoc(t)
	after, err := ParseBytes("api.proto", []byte(strings.Replace(protoSrc,
		"enum Status {\n  STATUS_UNSPECIFIED = 0;\n}",
		"enum Status {\n  STATUS_UNSPECIFIED = 0;\n  STATUS_PLACED = 1;\n}", 1)))
	if err != nil {
		t.Fatal(err)
	}
	b1, b2 := blockByID(before, "CreateOrderRequest/customer_id"), blockByID(after, "CreateOrderRequest/customer_id")
	if b2 == nil || b1.Hash != b2.Hash {
		t.Errorf("unrelated edit moved the field anchor: %+v vs %+v", b1, b2)
	}
	// A change to the field itself does go stale.
	renamed, err := ParseBytes("api.proto", []byte(strings.Replace(protoSrc, "string customer_id = 1;", "string customer_id = 4;", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if blockByID(renamed, "CreateOrderRequest/customer_id").Hash == b1.Hash {
		t.Error("renumbering a field left its hash unchanged")
	}
}

func TestProtoDuplicateDeclarationsGetUniqueIDs(t *testing.T) {
	doc, err := ParseBytes("dup.proto", []byte("message M {\n  string a = 1;\n  string a = 2;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if blockByID(doc, "M/a") == nil || blockByID(doc, "M/a#2") == nil {
		t.Errorf("duplicate field names collided: %+v", doc.Blocks)
	}
}

func TestProtoHTMLEscapesSource(t *testing.T) {
	doc, err := ParseBytes("x.proto", []byte("// <script>alert(1)</script>\nmessage M { map<string, string> m = 1; }\n"))
	if err != nil {
		t.Fatal(err)
	}
	html := doc.Blocks[0].HTML + doc.Blocks[1].HTML
	if strings.Contains(html, "<script>") {
		t.Error("comment text was not escaped")
	}
	if !strings.Contains(html, "map&lt;string, string&gt; m = 1;") {
		t.Errorf("declaration not escaped as expected: %q", html)
	}
}

func TestParseUnknownFormat(t *testing.T) {
	if _, err := ParseBytes("notes.rst", []byte("hi")); err == nil {
		t.Error("expected an error for an unparseable extension")
	}
	if got := FormatFor("Notes.MARKDOWN"); got != FormatMarkdown {
		t.Errorf("FormatFor is case-sensitive: %q", got)
	}
	if got := FormatFor("api.proto"); got != FormatProto {
		t.Errorf("FormatFor(.proto) = %q", got)
	}
}
