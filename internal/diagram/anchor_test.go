package diagram

import (
	"strings"
	"testing"
)

// realSVG is the shape of what mermaid-cli actually emits, trimmed to the
// parts anchoring depends on: an edge path naming its link twice, node groups
// named after the diagram's identifiers, a subgraph cluster, and a node that
// exists only because an edge mentioned it.
const realSVG = `<svg id="my-svg" width="600" xmlns="http://www.w3.org/2000/svg">
<g class="root"><g class="clusters">
<g class="cluster" id="my-svg-payments" data-look="classic"><rect x="1" y="2"/></g>
<g class="cluster-label" transform="translate(1,2)"><span>payments</span></g>
</g><g class="edgePaths">
<path d="M1,2" id="my-svg-L_client_api_0" class="flowchart-link" data-edge="true" data-id="L_client_api_0" marker-end="url(#my-svg_flowchart-v2-pointEnd)"/>
<path d="M3,4" id="my-svg-L_worker_queue_0" class="flowchart-link" data-id="L_worker_queue_0"/>
<path d="M5,6" id="my-svg-L_charge_invoice_0" class="flowchart-link" data-id="L_charge_invoice_0"/>
<path d="M7,8" id="my-svg-L_ghost_thing_0" class="flowchart-link" data-id="L_ghost_thing_0"/>
</g><g class="nodes">
<g class="node default" id="my-svg-flowchart-client-0" data-look="classic"><rect/></g>
<g class="node default" id="my-svg-flowchart-queue-3" data-look="classic"><rect/></g>
</g></g></svg>`

func flowAnchors() []Anchor {
	return []Anchor{
		{Block: "1/2/client-->api", Kind: "edge", Name: "client-->api"},
		{Block: "1/2/worker-->queue", Kind: "edge", Name: "worker-->queue"},
		{Block: "1/2/payments", Kind: "subgraph", Name: "payments"},
		{Block: "1/2/payments/charge-->invoice", Kind: "edge", Name: "charge-->invoice"},
		{Block: "1/2/queue", Kind: "node", Name: "queue"},
	}
}

// anchorOf returns the data-anchor of the tag whose id ends in want.
func anchorOf(t *testing.T, svg, want string) string {
	t.Helper()
	for _, tag := range strings.Split(svg, "<") {
		if !strings.Contains(tag, `id="`+want+`"`) {
			continue
		}
		i := strings.Index(tag, `data-anchor="`)
		if i < 0 {
			return ""
		}
		rest := tag[i+len(`data-anchor="`):]
		return rest[:strings.Index(rest, `"`)]
	}
	t.Fatalf("no tag with id %q in:\n%s", want, svg)
	return ""
}

func TestStampAnchorsEdgesNodesAndClusters(t *testing.T) {
	svg, matched := Stamp(realSVG, flowAnchors())
	if matched != 6 {
		t.Errorf("matched %d shapes, want 6 (3 edges, 2 boxes, 1 cluster)", matched)
	}
	for id, want := range map[string]string{
		"my-svg-L_client_api_0":     "1/2/client-->api",
		"my-svg-L_worker_queue_0":   "1/2/worker-->queue",
		"my-svg-L_charge_invoice_0": "1/2/payments/charge-->invoice",
		"my-svg-flowchart-queue-3":  "1/2/queue",
		"my-svg-payments":           "1/2/payments",
		// No statement declares the client box; the edge that named it does.
		"my-svg-flowchart-client-0": "1/2/client-->api",
	} {
		if got := anchorOf(t, svg, id); got != want {
			t.Errorf("%s: anchored %q, want %q", id, got, want)
		}
	}
	// A link the document never wrote as a statement, and a node that only
	// appears because an edge named it, stay unanchored: clicking them falls
	// through to the diagram itself.
	if got := anchorOf(t, svg, "my-svg-L_ghost_thing_0"); got != "" {
		t.Errorf("anchored an unknown edge as %q", got)
	}
	// The cluster's label group shares the word but is not the cluster.
	if strings.Count(svg, `data-anchor="1/2/payments"`) != 1 {
		t.Errorf("subgraph anchored more than once:\n%s", svg)
	}
	// Anchoring must not disturb the markup it stamps.
	if strings.Contains(svg, `class="node default" data-anchor`) {
		t.Error("anchor landed before the tag's own attributes")
	}
	// Nothing but the invisible hit copies is added, and no tag gains a
	// second class.
	if n, was := strings.Count(svg, `class="`), strings.Count(realSVG, `class="`); n != was+strings.Count(svg, `class="mg-hit"`) {
		t.Errorf("class attributes changed: %d, was %d", n, was)
	}
}

// A chain is one statement and draws as several paths, so every link in it
// points back at the statement the author wrote.
func TestStampChainSharesOneStatement(t *testing.T) {
	svg := `<svg id="s"><path id="s-L_a_b_0" data-id="L_a_b_0"/><path id="s-L_b_c_0" data-id="L_b_c_0"/></svg>`
	out, matched := Stamp(svg, []Anchor{{Block: "1/1/a-->b-->c", Kind: "edge", Name: "a-->b-->c"}})
	if matched != 2 {
		t.Fatalf("matched %d links of the chain, want 2", matched)
	}
	for _, id := range []string{"s-L_a_b_0", "s-L_b_c_0"} {
		if got := anchorOf(t, out, id); got != "1/1/a-->b-->c" {
			t.Errorf("%s anchored to %q", id, got)
		}
	}
}

// Without an svg id — a renderer configured differently — an edge is still
// named, and a cluster id is taken as written.
func TestStampWithoutRootID(t *testing.T) {
	svg := `<svg><path id="L_a_b_0"/><g class="cluster" id="pay"><rect/></g></svg>`
	out, matched := Stamp(svg, []Anchor{
		{Block: "1/1/a-->b", Name: "a-->b"},
		{Block: "1/1/pay", Name: "pay"},
	})
	if matched != 2 {
		t.Fatalf("matched %d, want 2:\n%s", matched, out)
	}
}

func TestStampNoAnchors(t *testing.T) {
	out, matched := Stamp(realSVG, nil)
	if matched != 0 || strings.Contains(out, "data-anchor") {
		t.Fatalf("stamped something with nothing to stamp: %d", matched)
	}
}

func TestEdgeBlockPeelsIndexAndUnderscores(t *testing.T) {
	edges := map[string]string{
		"client\x00api":    "1/1/client-->api",
		"a\x00b":           "1/1/a-->b",
		"order_svc\x00pay": "1/1/order_svc-->pay",
	}
	found := map[string]string{
		"client_api_0":    "1/1/client-->api",
		"a_b_12":          "1/1/a-->b",
		"order_svc_pay_0": "1/1/order_svc-->pay",
	}
	for id, want := range found {
		got, ok := edgeBlock(edges, id)
		if !ok || got != want {
			t.Errorf("edgeBlock(%q) = %q, %v; want %q", id, got, ok, want)
		}
	}
	for _, id := range []string{"short", "a_0", "x_y_0"} {
		if got, ok := edgeBlock(edges, id); ok {
			t.Errorf("edgeBlock(%q) resolved to %q", id, got)
		}
	}
}

// Identifiers with underscores are ambiguous in mermaid's own path ids; the
// anchor that exists wins over the one that does not.
func TestIndexResolvesUnderscoreAmbiguity(t *testing.T) {
	edges, nodes, mentions := index([]Anchor{
		{Block: "1/1/order_svc-->pay", Name: "order_svc-->pay"},
		{Block: "1/1/order_svc", Name: "order_svc"},
	})
	if got := edges["order_svc\x00pay"]; got != "1/1/order_svc-->pay" {
		t.Errorf("edge key missing, got %q from %v", got, edges)
	}
	if got := nodes["order_svc"]; got != "1/1/order_svc" {
		t.Errorf("node key missing, got %q", got)
	}
	if got := mentions["pay"]; got != "1/1/order_svc-->pay" {
		t.Errorf("a box only an edge names is unreachable, got %q", got)
	}
}

// A box mermaid drew because an edge named it is the biggest target in the
// picture; it has to open the statement that named it rather than nothing.
func TestStampAnchorsABoxAnEdgeNamed(t *testing.T) {
	svg := `<svg id="s"><path id="s-L_client_api_0"/><g class="node default" id="s-flowchart-api-1"><rect/></g></svg>`
	out, matched := Stamp(svg, []Anchor{{Block: "1/1/client-->api", Name: "client-->api"}})
	if matched != 2 {
		t.Fatalf("matched %d, want the edge and the box it named:\n%s", matched, out)
	}
	if got := anchorOf(t, out, "s-flowchart-api-1"); got != "1/1/client-->api" {
		t.Errorf("the box anchored to %q", got)
	}
}

// An arrow is two pixels wide; clicking it must not be. The invisible copy
// carries the same anchor along the same curve, and never paints.
func TestStampWidensEdgeTargets(t *testing.T) {
	out, _ := Stamp(realSVG, flowAnchors())
	if n := strings.Count(out, `class="mg-hit"`); n != 3 {
		t.Fatalf("%d hit targets for 3 anchored edges:\n%s", n, out)
	}
	if !strings.Contains(out, `<path class="mg-hit" d="M1,2" fill="none" stroke="transparent" stroke-width="16" pointer-events="stroke" data-anchor="1/2/client-->api"/>`) {
		t.Errorf("the hit target is not a faithful, invisible copy:\n%s", out)
	}
	// A path with no geometry gets no copy rather than an empty one.
	bare, _ := Stamp(`<svg id="s"><path id="s-L_a_b_0"/></svg>`, []Anchor{{Block: "1/1/a-->b", Name: "a-->b"}})
	if strings.Contains(bare, "mg-hit") {
		t.Errorf("copied a path with no d:\n%s", bare)
	}
}

// Mermaid closes its path tags; the anchor must not break them.
func TestMarkKeepsSelfClosingTags(t *testing.T) {
	if got := mark(`<path d="M1,2"/>`, "1/1/a-->b"); got != `<path d="M1,2" data-anchor="1/1/a-->b"/>` {
		t.Errorf("self-closing tag became %q", got)
	}
	if got := mark(`<g class="node">`, "1/1/a"); got != `<g class="node" data-anchor="1/1/a">` {
		t.Errorf("open tag became %q", got)
	}
}

// An edge's label sits on top of the middle of its arrow, which is exactly
// where a reviewer points. It has to answer for the statement the arrow is.
func TestStampAnchorsEdgeLabels(t *testing.T) {
	svg := `<svg id="s"><g class="edgePaths"><path id="s-L_worker_queue_0" d="M1,2"/></g>` +
		`<g class="edgeLabels"><g class="edgeLabel"><g class="label" data-id="L_worker_queue_0" transform="translate(1,2)"><span>retry</span></g></g></g></svg>`
	out, matched := Stamp(svg, []Anchor{{Block: "1/1/worker-->queue", Name: "worker-->queue"}})
	if matched != 2 {
		t.Fatalf("matched %d, want the arrow and its label:\n%s", matched, out)
	}
	if !strings.Contains(out, `<g class="label" data-id="L_worker_queue_0" transform="translate(1,2)" data-anchor="1/1/worker-->queue">`) {
		t.Errorf("the label is not anchored:\n%s", out)
	}
	// A node's inner label group has no link id and must stay untouched.
	plain, _ := Stamp(`<svg id="s"><g class="node" id="s-flowchart-a-0"><g class="label" transform="translate(1,2)"/></g></svg>`,
		[]Anchor{{Block: "1/1/a", Name: "a"}})
	if strings.Count(plain, "data-anchor") != 1 {
		t.Errorf("stamped a node's own label group:\n%s", plain)
	}
}
