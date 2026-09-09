package protoschema

import "testing"

const sample = `syntax = "proto3";

package orders.v1;

import "google/protobuf/timestamp.proto";

option go_package = "example.com/orders/v1;ordersv1";
option (custom.file_rule) = { level: STRICT, tags: { a: 1 } };

// An order the customer wants placed.
message CreateOrderRequest {
  // Stable customer id.
  string customer_id = 1;
  repeated Line lines = 2;  // at least one
  reserved 4, 7 to 9;

  /* A single ordered item. */
  message Line {
    string sku = 1;
    int32 qty = 2 [deprecated = true];
  }

  oneof payment {
    Card card = 5;
    string invoice_ref = 6;
  }

  map<string, string> labels = 10;
}

enum Status {
  STATUS_UNSPECIFIED = 0;
  STATUS_PLACED = 1;
}

service OrderService {
  rpc CreateOrder(CreateOrderRequest) returns (Card);
  rpc StreamOrders(stream CreateOrderRequest) returns (stream Card) {
    option idempotency_level = NO_SIDE_EFFECTS;
  }
}
`

// find returns the first declaration of the given kind and name, searching
// the whole tree.
func find(decls []*Decl, kind, name string) *Decl {
	for _, d := range decls {
		if d.Kind == kind && d.Name == name {
			return d
		}
		if got := find(d.Children, kind, name); got != nil {
			return got
		}
	}
	return nil
}

func parseSample(t *testing.T) *File {
	t.Helper()
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestParseFileHeader(t *testing.T) {
	f := parseSample(t)
	if f.Syntax != "proto3" {
		t.Errorf("syntax = %q, want proto3", f.Syntax)
	}
	if f.Package != "orders.v1" {
		t.Errorf("package = %q, want orders.v1", f.Package)
	}
	if d := find(f.Decls, KindImport, "google/protobuf/timestamp.proto"); d == nil {
		t.Error("import not parsed")
	}
	if d := find(f.Decls, KindOption, "go_package"); d == nil {
		t.Error("option name not parsed")
	}
}

// An aggregate option value carries braces of its own; they must not be read
// as a nested declaration scope.
func TestParseAggregateOptionStaysOneDeclaration(t *testing.T) {
	f := parseSample(t)
	d := find(f.Decls, KindOption, "(custom.file_rule)")
	if d == nil {
		t.Fatal("aggregate option not parsed")
	}
	if len(d.Children) != 0 {
		t.Errorf("aggregate option got %d children, want 0", len(d.Children))
	}
	if d.Text != "option (custom.file_rule) = { level: STRICT, tags: { a: 1 } };" {
		t.Errorf("option text = %q", d.Text)
	}
	// The message after it must still be a top-level declaration.
	if find(f.Decls, KindMessage, "CreateOrderRequest") == nil {
		t.Error("declaration after aggregate option was swallowed")
	}
}

func TestParseFieldsAndComments(t *testing.T) {
	f := parseSample(t)
	cases := []struct{ name, text, comment, trailing string }{
		{"customer_id", "string customer_id = 1;", "Stable customer id.", ""},
		{"lines", "repeated Line lines = 2;", "", "at least one"},
		{"qty", "int32 qty = 2 [deprecated = true];", "", ""},
		{"labels", "map<string, string> labels = 10;", "", ""},
	}
	for _, c := range cases {
		d := find(f.Decls, KindField, c.name)
		if d == nil {
			t.Errorf("field %s not parsed", c.name)
			continue
		}
		if d.Text != c.text {
			t.Errorf("field %s text = %q, want %q", c.name, d.Text, c.text)
		}
		if d.Comment != c.comment {
			t.Errorf("field %s comment = %q, want %q", c.name, d.Comment, c.comment)
		}
		if d.Trailing != c.trailing {
			t.Errorf("field %s trailing = %q, want %q", c.name, d.Trailing, c.trailing)
		}
	}
}

func TestParseNesting(t *testing.T) {
	f := parseSample(t)
	msg := find(f.Decls, KindMessage, "CreateOrderRequest")
	if msg == nil {
		t.Fatal("message not parsed")
	}
	if msg.Comment != "An order the customer wants placed." {
		t.Errorf("message comment = %q", msg.Comment)
	}
	if msg.Text != "message CreateOrderRequest" {
		t.Errorf("message text = %q, want the header only", msg.Text)
	}
	line := find(msg.Children, KindMessage, "Line")
	if line == nil || len(line.Children) != 2 {
		t.Fatalf("nested message Line = %+v", line)
	}
	if line.Comment != "A single ordered item." {
		t.Errorf("block comment = %q", line.Comment)
	}
	oneof := find(msg.Children, KindOneof, "payment")
	if oneof == nil || len(oneof.Children) != 2 {
		t.Fatalf("oneof payment = %+v", oneof)
	}
	if find(oneof.Children, KindField, "invoice_ref") == nil {
		t.Error("oneof member not parsed as a field")
	}
	if find(msg.Children, KindReserved, "") == nil {
		t.Error("reserved range not parsed")
	}
}

func TestParseEnumAndService(t *testing.T) {
	f := parseSample(t)
	enum := find(f.Decls, KindEnum, "Status")
	if enum == nil || len(enum.Children) != 2 {
		t.Fatalf("enum Status = %+v", enum)
	}
	if v := find(enum.Children, KindEnumValue, "STATUS_UNSPECIFIED"); v == nil || v.Text != "STATUS_UNSPECIFIED = 0;" {
		t.Errorf("enum zero value = %+v", v)
	}
	svc := find(f.Decls, KindService, "OrderService")
	if svc == nil || len(svc.Children) != 2 {
		t.Fatalf("service = %+v", svc)
	}
	rpc := find(svc.Children, KindRPC, "StreamOrders")
	if rpc == nil {
		t.Fatal("streaming rpc not parsed")
	}
	if rpc.Text != "rpc StreamOrders(stream CreateOrderRequest) returns (stream Card)" {
		t.Errorf("rpc text = %q", rpc.Text)
	}
	if len(rpc.Children) != 1 || rpc.Children[0].Kind != KindOption {
		t.Errorf("rpc body = %+v", rpc.Children)
	}
}

func TestParseLineNumbers(t *testing.T) {
	f := parseSample(t)
	if got := find(f.Decls, KindField, "customer_id"); got == nil || got.Line != 13 {
		t.Errorf("customer_id line = %+v, want 13", got)
	}
}

// Marginalia must render a file protoc would reject: the point is to review it
// before it compiles cleanly.
func TestParseTolerantOfMalformedInput(t *testing.T) {
	f, err := Parse([]byte("message Broken {\n  string ok = 1;\n  gibberish here\n}\n}\nenum E { A = 0; }\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if find(f.Decls, KindField, "ok") == nil {
		t.Error("good field lost to bad neighbour")
	}
	if find(f.Decls, KindUnknown, "") == nil {
		t.Error("unclassifiable statement should surface as unknown, not vanish")
	}
	if find(f.Decls, KindEnum, "E") == nil {
		t.Error("declarations after a stray brace should still parse")
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse([]byte("// just a comment\n")); err == nil {
		t.Error("expected error for a file with no declarations")
	}
}

func TestParseStringsAndProse(t *testing.T) {
	f, err := Parse([]byte("option x = \"a;b{c}\";\nmessage M { string s = 1 [(v) = 'q;q']; }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d := find(f.Decls, KindOption, "x"); d == nil || d.Text != `option x = "a;b{c}";` {
		t.Errorf("semicolons and braces inside a string ended the statement: %+v", d)
	}
	if d := find(f.Decls, KindField, "s"); d == nil {
		t.Error("field with a single-quoted option value not parsed")
	}
}
