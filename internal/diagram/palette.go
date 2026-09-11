package diagram

import (
	"regexp"
	"strings"
)

// Mermaid inlines its own stylesheet into every SVG it renders, keyed on the
// svg's id — `#my-svg .node rect{fill:#ECECFF;stroke:#9370DB}`. An id selector
// outranks anything our page can write, so a page that wants the drawing in
// the reviewer's colours has two options: shout over it with !important, or
// answer it. This answers it.
//
// We already own these bytes: sanitize rewrites them before they reach the
// page. Here the colours mermaid chose are replaced by the custom properties
// the page defines from the active theme, so the SVG arrives already speaking
// light or dark — which a server-side render could never decide, since the
// reviewer picks the mode long after it ran. Custom properties also inherit
// and carry no specificity, so hovering a shape can recolour it by setting one
// on the shape, with no specificity fight at all.
//
// Everything else — every position, every size, every rule whose selector we
// do not recognise — is left exactly as mermaid wrote it. A diagram in the
// wrong colours is a bad picture; a diagram we broke trying to recolour it is
// no picture.

var (
	styleBlock = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	// One declaration: property, value, and whatever separates them.
	declaration = regexp.MustCompile(`(?is)([-a-z]+)(\s*:\s*)([^;]+)`)
)

// The roles a rule can have. Which one a selector plays is all we need to know
// to recolour it: mermaid says what a thing is in its class names.
const (
	roleUnknown = iota
	roleSurface // a node's box
	roleCluster // a subgraph's frame
	roleText    // a label, in any of the several ways mermaid draws one
	roleLabelBg // the patch a label sits on, where it overlaps a line
	roleLine    // an edge, and the arrowhead that ends it
	roleBad     // mermaid's own error styling
)

// The page defines these from the theme; see review.html.tmpl. Naming them
// here keeps the two ends of the contract in one searchable place.
const (
	varNodeFill   = "var(--mg-node-fill)"
	varNodeStroke = "var(--mg-node-stroke)"
	varClusterBg  = "var(--mg-cluster-fill)"
	varText       = "var(--mg-text)"
	varLabelBg    = "var(--mg-label-bg)"
	varLine       = "var(--mg-line)"
	varBad        = "var(--mg-bad)"
)

// theme rewrites the palette of every stylesheet inlined in an SVG.
func theme(svg string) string {
	return styleBlock.ReplaceAllStringFunc(svg, func(block string) string {
		m := styleBlock.FindStringSubmatch(block)
		if m == nil {
			return block
		}
		return strings.Replace(block, m[1], themeCSS(m[1]), 1)
	})
}

// themeCSS walks a stylesheet rule by rule. It is not a CSS parser: it finds
// the brace depth, hands each top-level rule's declarations to substitute, and
// copies at-rules (mermaid's keyframes) through untouched, because nothing in
// them is a colour we asked about.
func themeCSS(css string) string {
	var out strings.Builder
	for i := 0; i < len(css); {
		open := strings.IndexByte(css[i:], '{')
		if open < 0 {
			out.WriteString(css[i:])
			break
		}
		open += i
		selector := css[i:open]
		body, end := blockAt(css, open)
		out.WriteString(selector)
		if strings.HasPrefix(strings.TrimSpace(selector), "@") {
			out.WriteString(body)
		} else {
			out.WriteString("{" + substitute(inner(body), roleOf(selector)) + "}")
		}
		i = end
	}
	return out.String()
}

// blockAt returns the braced block starting at open, braces included, and the
// index just past it. An unclosed block runs to the end, which is what a
// truncated stylesheet deserves.
func blockAt(css string, open int) (block string, end int) {
	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return css[open : i+1], i + 1
			}
		}
	}
	return css[open:], len(css)
}

func inner(block string) string {
	return strings.TrimSuffix(strings.TrimPrefix(block, "{"), "}")
}

// roleOf reads what a rule is about off its selector. Order matters: a
// cluster's label is text, not a cluster, and an edge's label is a patch on a
// line rather than the line.
func roleOf(selector string) int {
	s := strings.ToLower(selector)
	switch {
	case strings.Contains(s, "error"):
		return roleBad
	case strings.Contains(s, "labelbkg"), strings.Contains(s, "edgelabel"):
		return roleLabelBg
	case strings.Contains(s, "label"), strings.Contains(s, "text"),
		strings.Contains(s, "span"), strings.Contains(s, "tspan"):
		return roleText
	case strings.Contains(s, "cluster"):
		return roleCluster
	case strings.Contains(s, "node"):
		return roleSurface
	case strings.Contains(s, "link"), strings.Contains(s, "edge"),
		strings.Contains(s, "marker"), strings.Contains(s, "arrowhead"),
		strings.Contains(s, "messageline"):
		return roleLine
	case isRoot(s):
		// The svg's own rule, which is where mermaid puts the colour every
		// piece of text inherits.
		return roleText
	}
	return roleUnknown
}

// isRoot reports whether a selector is the svg element itself — one id, no
// descendant, no class of its own.
func isRoot(selector string) bool {
	s := strings.TrimSpace(selector)
	if !strings.HasPrefix(s, "#") {
		return false
	}
	return !strings.ContainsAny(s, " \t\n,.>[:")
}

// substitute swaps the colour in each declaration for the page's variable for
// that role. A property that is not a colour, a value that is not one
// (`none`, `inherit`, a variable already), and a rule whose role we could not
// read are all left as they are.
func substitute(decls string, role int) string {
	if role == roleUnknown {
		return decls
	}
	return declaration.ReplaceAllStringFunc(decls, func(decl string) string {
		m := declaration.FindStringSubmatch(decl)
		if m == nil {
			return decl
		}
		property, value := strings.ToLower(strings.TrimSpace(m[1])), strings.TrimSpace(m[3])
		if !isColour(value) {
			return decl
		}
		replacement := colourFor(role, property)
		if replacement == "" {
			return decl
		}
		return m[1] + m[2] + replacement + importantOf(value)
	})
}

// colourFor is the whole mapping: the property being set says whether it
// paints a surface or draws a line, the role says which thing it belongs to,
// and together they name the variable that answers. A property that is not a
// colour never reaches a variable at all — that is what keeps `text-align` and
// `stroke-width` out of this.
func colourFor(role int, property string) string {
	switch property {
	case "fill", "color", "stop-color", "background", "background-color":
		switch role {
		case roleSurface:
			return varNodeFill
		case roleCluster:
			return varClusterBg
		case roleText:
			return varText
		case roleLabelBg:
			return varLabelBg
		case roleLine:
			// An arrowhead is filled with the colour its line is stroked.
			return varLine
		case roleBad:
			return varBad
		}
	case "stroke":
		switch role {
		case roleSurface, roleCluster:
			return varNodeStroke
		case roleText:
			return varText
		case roleLine:
			return varLine
		case roleBad:
			return varBad
		}
	}
	return ""
}

// isColour reports whether a value is a colour we should replace. It is only
// ever asked about the value of a property colourFor recognised, so a bare
// word here is a named colour. Keywords that mean "do not paint" carry meaning
// mermaid relies on for layout, and a value that is already a variable has
// been through here before.
func isColour(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.TrimSpace(strings.TrimSuffix(v, "!important"))
	switch v {
	case "", "none", "transparent", "inherit", "initial", "unset", "currentcolor":
		return false
	}
	if strings.HasPrefix(v, "var(") || strings.HasPrefix(v, "url(") {
		return false
	}
	if strings.HasPrefix(v, "#") || strings.HasPrefix(v, "rgb") || strings.HasPrefix(v, "hsl") {
		return true
	}
	// A bare word is a named colour only if it is not a keyword of some other
	// property: every colour property mermaid sets that is not a function or a
	// hash is a name like "black" or "white".
	return !strings.ContainsAny(v, " (),%") && !strings.ContainsAny(v, "0123456789")
}

func importantOf(value string) string {
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(value)), "!important") {
		return "!important"
	}
	return ""
}
