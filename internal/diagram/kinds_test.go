package diagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// Issue #153: real mmdc 12.0.0 renders of a sequence, a state and an ER
// diagram, driven through the whole pipeline like the flowchart fixture.
// Shapes are named by id where mermaid gives one, and otherwise by
// data-et:data-id, since a message or note has no id of its own.
var kindFixtures = []struct {
	name   string
	shapes map[string]string // shape → the statement it must answer for
	source []string          // statements with no shape, on purpose
}{
	{"sequence", map[string]string{
		"participant:C":        "C",
		"participant:A":        "A",
		"participant:DB":       "A->DB", // undeclared: the message that named it
		"message:i0":           "C->A",
		"message:i1":           "A->C",
		"message:i4":           "loop/C->A",
		"message:i6":           "A->DB",
		"note:i2":              "note",
		"control-structure:i5": "loop",
		// nested groups are drawn as they close: the inner one first
		"message:i9":            "alt/opt/A->DB",
		"control-structure:i10": "alt/opt",
		"control-structure:i11": "alt",
	}, nil},
	{"state", map[string]string{
		"my-svg-state-Pending-1": "[*]-->Pending",
		"my-svg-state-Paid-2":    "Paid",
		"my-svg-state-Shipped-3": "Paid-->Shipped",
		"my-svg-edge0":           "[*]-->Pending",
		"my-svg-edge1":           "Pending-->Paid",
		"my-svg-edge2":           "Paid-->Shipped",
		"my-svg-edge3":           "Shipped-->[*]",
	}, nil},
	{"er", map[string]string{
		"my-svg-entity-CUSTOMER-0":                      "CUSTOMER",
		"my-svg-entity-ORDER-1":                         "CUSTOMER--ORDER",
		"my-svg-entity-LINE-ITEM-2":                     "ORDER--LINE-ITEM",
		"my-svg-id_entity-CUSTOMER-0_entity-ORDER-1_0":  "CUSTOMER--ORDER",
		"my-svg-id_entity-ORDER-1_entity-LINE-ITEM-2_1": "ORDER--LINE-ITEM",
		// a second relationship between the same pair, and its own label
		"my-svg-id_entity-CUSTOMER-0_entity-ORDER-1_2": "CUSTOMER--ORDER#2",
	}, []string{"CUSTOMER/name"}}, // an attribute is a row inside its entity's box
}

func renderKind(t *testing.T, name, svg string) (*document.Document, string) {
	t.Helper()
	source, err := os.ReadFile("testdata/" + name + ".mmd")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name+".mmd")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeMMDC(t, svg)
	if n, failures := Attach(doc, &Renderer{Bin: bin, Cache: t.TempDir()}); n != 1 || len(failures) != 0 {
		t.Fatalf("rendered %d, failures %v", n, failures)
	}
	return doc, doc.Blocks[0].SVG
}

// shapeAnchors maps each anchored shape's name to its anchor.
func shapeAnchors(svg string) map[string]string {
	out := map[string]string{}
	for _, tag := range shapeTag.FindAllString(svg, -1) {
		a := attr(tag, "data-anchor")
		if a == "" || strings.Contains(tag, "mg-hit") {
			continue
		}
		key := attr(tag, "id")
		if et := attr(tag, "data-et"); et != "" && et != "edge" {
			key = et + ":" + attr(tag, "data-id")
		}
		if key != "" {
			out[key] = a
		}
	}
	return out
}

func TestRealKindsAreAnchored(t *testing.T) {
	for _, f := range kindFixtures {
		t.Run(f.name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + f.name + "-mermaid-cli-12.0.0.svg")
			if err != nil {
				t.Fatal(err)
			}
			doc, svg := renderKind(t, f.name, string(raw))
			got := shapeAnchors(svg)
			for shape, want := range f.shapes {
				if got[shape] != want {
					t.Errorf("%s anchored to %q, want %q", shape, got[shape], want)
				}
			}
			// Every statement is reachable in the picture, but the ones
			// named as source-only.
			for _, b := range doc.Blocks[1:] {
				reached := strings.Contains(svg, `data-anchor="`+b.ID+`"`)
				skip := false
				for _, s := range f.source {
					skip = skip || s == b.ID
				}
				if !reached && !skip && b.Kind != "participant" {
					t.Errorf("statement %q (%s) has no shape in the drawing", b.ID, b.Kind)
				}
			}
		})
	}
}

// A drawing that disagrees with the source on how many messages there are
// is not paired by position: nothing is better than the wrong one.
func TestKindsRefuseAMiscount(t *testing.T) {
	raw, err := os.ReadFile("testdata/sequence-mermaid-cli-12.0.0.svg")
	if err != nil {
		t.Fatal(err)
	}
	// Drop the second C->A message from the drawing.
	svg := strings.Replace(string(raw), `data-id="i4" data-from="C" data-to="A"`, `data-id="i4" data-from="X" data-to="Y"`, 1)
	_, out := renderKind(t, "sequence", svg)
	if got := shapeAnchors(out)["message:i0"]; got != "" {
		t.Errorf("message paired by position despite a miscount: %q", got)
	}
}

// Each label of a repeated ER pair answers for its own relationship, not
// the pair's first.
func TestRepeatedPairLabelsKeepTheirOrder(t *testing.T) {
	raw, err := os.ReadFile("testdata/er-mermaid-cli-12.0.0.svg")
	if err != nil {
		t.Fatal(err)
	}
	_, svg := renderKind(t, "er", string(raw))
	for _, tag := range shapeTag.FindAllString(svg, -1) {
		if !strings.HasPrefix(tag, "<g") || attr(tag, "data-id") != "id_entity-CUSTOMER-0_entity-ORDER-1_2" {
			continue
		}
		if got := attr(tag, "data-anchor"); got != "CUSTOMER--ORDER#2" {
			t.Errorf("the second relationship's label answers for %q", got)
		}
		return
	}
	t.Fatal("the fixture no longer labels the second CUSTOMER--ORDER relationship")
}

// A block id is written as HTML, not as a Go string: a quote must not end
// the attribute, and a backslash is just a backslash.
func TestAnchorsAreHTMLEscaped(t *testing.T) {
	got := mark(`<g class="node">`, `say "hi"\&`)
	if want := `<g class="node" data-anchor="say &quot;hi&quot;\&amp;">`; got != want {
		t.Errorf("mark wrote %s, want %s", got, want)
	}
	line := hitLine(`<line x1="1" y1="2" x2="3" y2="4">`, `a"b`)
	if !strings.Contains(line, `data-anchor="a&quot;b"`) {
		t.Errorf("hitLine wrote %s", line)
	}
}
