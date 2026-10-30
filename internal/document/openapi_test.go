package document

import (
	"strings"
	"testing"
)

const ordersAPI = `openapi: 3.1.0
info:
  title: Orders
paths:
  /orders/{id}:
    parameters:
      - name: id
        in: path
    get:
      summary: Fetch one order
      responses:
        "200":
          description: The order
    delete:
      summary: Cancel it
components:
  schemas:
    Order:
      type: object
`

// Issue #150: operations anchor by method and path, components by kind and
// name, everything else as the generic tree.
func TestOpenAPIAnchorsByOperation(t *testing.T) {
	doc, err := ParseBytes("api.yaml", []byte(ordersAPI))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ids(doc), "\n")
	for _, want := range []string{
		"$.info.title",
		"/orders/{id}",
		"/orders/{id}.parameters[0].name",
		"GET /orders/{id}",
		"GET /orders/{id}.summary",
		`GET /orders/{id}.responses["200"].description`,
		"DELETE /orders/{id}",
		"components/schemas",
		"components/schemas/Order",
		"components/schemas/Order.type",
	} {
		if !strings.Contains("\n"+got+"\n", "\n"+want+"\n") {
			t.Errorf("no block %q in:\n%s", want, got)
		}
	}
	get := block(t, doc, "GET /orders/{id}")
	if get.Parent != "/orders/{id}" || get.Level != 3 || !get.HasChildren {
		t.Errorf("operation under %q at level %d, children %v", get.Parent, get.Level, get.HasChildren)
	}
	// The same spec in JSON anchors the same way.
	j, err := ParseBytes("api.json", []byte(`{"swagger":"2.0","paths":{"/a":{"post":{}}},"definitions":{"A":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"POST /a", "definitions/A"} {
		block(t, j, want)
	}
}

// Adding an operation leaves every other operation's id and hash alone.
func TestOpenAPIEditsElsewhereMoveNothing(t *testing.T) {
	before, _ := ParseBytes("api.yaml", []byte(ordersAPI))
	edited := strings.Replace(ordersAPI, "components:", "  /health:\n    get:\n      summary: up\ncomponents:", 1)
	after, _ := ParseBytes("api.yaml", []byte(edited))
	now := map[string]string{}
	for _, b := range after.Blocks {
		now[b.ID] = b.Hash
	}
	for _, b := range before.Blocks {
		if b.ID == "$" || b.ID == "$.paths" {
			continue // their child counts changed, which is what they say
		}
		if now[b.ID] != b.Hash {
			t.Errorf("%s moved or changed", b.ID)
		}
	}
	block(t, after, "GET /health")
}

// A spec whose shape is off still renders, as the generic tree where it is
// off; a file without the marker key is the generic tree it always was.
func TestOpenAPIMalformedAndPlain(t *testing.T) {
	doc, err := ParseBytes("api.yaml", []byte("openapi: 3.0.0\npaths:\n  - not a map\ncomponents: 7\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$.paths[0]", "$.components"} {
		block(t, doc, want)
	}
	doc, _ = ParseBytes("api.yaml", []byte("openapi: 3.0.0\ncomponents:\n  schemas:\n    - a\n"))
	for _, want := range []string{"components/schemas[0]"} {
		block(t, doc, want)
	}
	plain, _ := ParseBytes("c.yaml", []byte("paths:\n  /a:\n    get: {}\n"))
	block(t, plain, `$.paths["/a"].get`)
}
