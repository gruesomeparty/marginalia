package diagram

import (
	"strings"
	"testing"
)

// mermaidCSS is the shape of the stylesheet mermaid inlines: keyed on the
// svg's id, with keyframes, a root rule that colours every label by
// inheritance, and per-part rules.
const mermaidCSS = `#my-svg{font-family:"trebuchet ms",verdana,arial,sans-serif;font-size:16px;fill:#333;}` +
	`@keyframes edge-animation-frame{from{stroke-dashoffset:0;}}` +
	`#my-svg .error-text{fill:#552222;stroke:#552222;}` +
	`#my-svg .edge-thickness-normal{stroke-width:1px;}` +
	`#my-svg .marker{fill:#333333;stroke:#333333;}` +
	`#my-svg .label{font-family:"trebuchet ms";color:#333;}` +
	`#my-svg .label text,#my-svg span{fill:#333;color:#333;}` +
	`#my-svg .node rect,#my-svg .node circle,#my-svg .node ellipse,#my-svg .node polygon,#my-svg .node path{fill:#ECECFF;stroke:#9370DB;stroke-width:1px;}` +
	`#my-svg .flowchart-link{stroke:#333333;fill:none;}` +
	`#my-svg .edgeLabel{background-color:rgba(232,232,232, 0.8);text-align:center;}` +
	`#my-svg .edgeLabel rect{opacity:0.5;fill:rgba(232,232,232, 0.8);}` +
	`#my-svg .labelBkg{background-color:rgba(232, 232, 232, 0.5);}` +
	`#my-svg .cluster rect{fill:#ffffde;stroke:#aaaa33;stroke-width:1px;}` +
	`#my-svg .cluster-label span{color:#333;}`

func TestThemeCSSSpeaksThePagesColours(t *testing.T) {
	out := themeCSS(mermaidCSS)
	for _, want := range []string{
		// a node is a surface with an outline
		`.node rect,#my-svg .node circle,#my-svg .node ellipse,#my-svg .node polygon,#my-svg .node path{fill:var(--mg-node-fill);stroke:var(--mg-node-stroke);stroke-width:1px;}`,
		// an edge, and the arrowhead that ends it, are the same line
		`.flowchart-link{stroke:var(--mg-line);fill:none;}`,
		`.marker{fill:var(--mg-line);stroke:var(--mg-line);}`,
		// every way mermaid draws a label ends up as text
		`.label text,#my-svg span{fill:var(--mg-text);color:var(--mg-text);}`,
		`.cluster-label span{color:var(--mg-text);}`,
		// the patch a label sits on is a background, not a line
		`.edgeLabel{background-color:var(--mg-label-bg);text-align:center;}`,
		`.edgeLabel rect{opacity:0.5;fill:var(--mg-label-bg);}`,
		`.labelBkg{background-color:var(--mg-label-bg);}`,
		// a subgraph frame
		`.cluster rect{fill:var(--mg-cluster-fill);stroke:var(--mg-node-stroke);stroke-width:1px;}`,
		// the svg's own rule is where inherited text colour lives
		`#my-svg{font-family:"trebuchet ms",verdana,arial,sans-serif;font-size:16px;fill:var(--mg-text);}`,
		`.error-text{fill:var(--mg-bad);stroke:var(--mg-bad);}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing:\n  %s\ngot:\n  %s", want, out)
		}
	}
	// Nothing that is not a colour is touched.
	if !strings.Contains(out, `.edge-thickness-normal{stroke-width:1px;}`) {
		t.Error("a rule with no colour in it was rewritten")
	}
	if !strings.Contains(out, `@keyframes edge-animation-frame{from{stroke-dashoffset:0;}}`) {
		t.Error("an at-rule was not copied through")
	}
	if strings.Contains(out, "#ECECFF") || strings.Contains(out, "#9370DB") || strings.Contains(out, "rgba(232") {
		t.Errorf("a mermaid colour survived:\n%s", out)
	}
	// `fill:none` is layout, not palette: mermaid needs it to keep a link from
	// painting its interior.
	if strings.Count(out, "fill:none") != 1 {
		t.Error("fill:none was replaced with a colour")
	}
}

// A rule about something we have no variable for keeps its own colours. Half a
// recolour is worse than none, and a broken picture is worse than either.
func TestThemeCSSLeavesUnknownRulesAlone(t *testing.T) {
	css := `#my-svg .someNewThing rect{fill:#123456;}#my-svg .node rect{fill:#ECECFF;}`
	out := themeCSS(css)
	if !strings.Contains(out, `.someNewThing rect{fill:#123456;}`) {
		t.Errorf("rewrote a rule it does not understand:\n%s", out)
	}
	if !strings.Contains(out, `.node rect{fill:var(--mg-node-fill);}`) {
		t.Errorf("did not rewrite the rule it does understand:\n%s", out)
	}
}

func TestThemeRewritesOnlyTheStylesheet(t *testing.T) {
	svg := `<svg id="my-svg"><style>#my-svg .node rect{fill:#ECECFF;}</style>` +
		`<g class="node" id="my-svg-flowchart-a-0"><rect x="1" y="2" fill="#ECECFF"/></g></svg>`
	out := theme(svg)
	if !strings.Contains(out, `<style>#my-svg .node rect{fill:var(--mg-node-fill);}</style>`) {
		t.Errorf("stylesheet not rewritten:\n%s", out)
	}
	// A presentation attribute is mermaid's own decision about one element,
	// and rewriting attributes is how a recolour turns into a redraw.
	if !strings.Contains(out, `<rect x="1" y="2" fill="#ECECFF"/>`) {
		t.Errorf("an element's own attributes were touched:\n%s", out)
	}
	// An SVG with no stylesheet is passed through whole.
	bare := `<svg id="s"><rect/></svg>`
	if theme(bare) != bare {
		t.Errorf("an SVG with no stylesheet came back changed: %s", theme(bare))
	}
}

func TestThemeSurvivesATruncatedStylesheet(t *testing.T) {
	out := themeCSS(`#my-svg .node rect{fill:#ECECFF;stroke:#9370DB`)
	if !strings.Contains(out, "var(--mg-node-fill)") {
		t.Errorf("gave up on an unclosed rule:\n%s", out)
	}
}

func TestIsColour(t *testing.T) {
	for _, yes := range []string{"#333", "#ECECFF", "rgba(232,232,232,.8)", "rgb(1,2,3)", "hsl(1,2%,3%)", "black", "#333 !important"} {
		if !isColour(yes) {
			t.Errorf("isColour(%q) = false", yes)
		}
	}
	for _, no := range []string{"none", "transparent", "inherit", "currentColor", "var(--mg-text)", "url(#grad)", "1px", "0.5", ""} {
		if isColour(no) {
			t.Errorf("isColour(%q) = true", no)
		}
	}
}

// A colour mermaid marked !important keeps its weight when it is replaced —
// dropping it would change which rule wins inside mermaid's own stylesheet.
// A property that is not a colour is never replaced, whatever the rule is
// about: mermaid's layout is in those values.
func TestSubstituteLeavesNonColourProperties(t *testing.T) {
	out := substitute(`stroke-width:1px;text-align:center;opacity:0.5;stroke:#333`, roleLine)
	for _, want := range []string{"stroke-width:1px", "text-align:center", "opacity:0.5", "stroke:var(--mg-line)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestSubstituteKeepsImportant(t *testing.T) {
	out := substitute(`stroke:#333!important;fill:none`, roleLine)
	if !strings.Contains(out, "stroke:var(--mg-line)!important") {
		t.Errorf("lost !important: %s", out)
	}
}
