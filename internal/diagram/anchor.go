package diagram

import (
	"fmt"
	"regexp"
	"strings"
)

// Mermaid's SVG is anchorable, which is what makes a picture worth rendering
// for a review tool rather than just for reading: it names its own parts.
//
//	<path id="L_client_api_0" …>                    an edge
//	<g class="node …" id="my-svg-flowchart-api-1">   a node
//
// Those names come from the diagram's own identifiers, so they map onto the
// block paths the parser already assigned — `1/4/client-->api` for the edge,
// `1/4/api` for the node if the document declared it. Stamping the mapping
// onto the shapes turns the drawing into the review surface: the reviewer
// clicks the arrow that is wrong, and the note lands on that statement.

var (
	// An edge path carries the link's name as `L_<from>_<to>_<n>`, in `id`
	// prefixed with the svg's own id and again, bare, in `data-id`. Either
	// spelling is read, and the prefix is peeled off.
	edgePath = regexp.MustCompile(`(?is)<path\b[^>]*\s(?:data-)?id="(?:[^"]*-)?L_([^"]+)"[^>]*>`)
	// Nodes and subgraph clusters are groups; which one a group is comes off
	// its class, and its name off its id.
	groupTag = regexp.MustCompile(`(?is)<g\b[^>]*>`)
	// An edge's label is drawn over the middle of its arrow and takes the
	// pointer there, so it has to answer for the same statement. Mermaid
	// names it with the link's id.
	edgeLabelGroup = regexp.MustCompile(`(?is)<g\b[^>]*\sdata-id="(?:[^"]*-)?L_([^"]+)"[^>]*>`)
	classAttr      = regexp.MustCompile(`(?is)\sclass="([^"]*)"`)
	// Deliberately whitespace, not a word boundary: `data-id` is a different
	// attribute and mermaid puts both on the same tag.
	idAttr   = regexp.MustCompile(`(?is)\sid="([^"]*)"`)
	flowNode = regexp.MustCompile(`(?i)(?:^|-)flowchart-(.+)-\d+$`)
	rootSVG  = regexp.MustCompile(`(?is)<svg\b[^>]*\sid="([^"]*)"`)
	dAttr    = regexp.MustCompile(`(?is)\sd="([^"]*)"`)
)

// Anchor is one statement of the diagram as the page knows it.
type Anchor struct {
	Block string // the block id, e.g. 1/4/client-->api
	Kind  string // the statement kind: edge, node, subgraph…
	Name  string // the statement's own path segment, e.g. client-->api
}

// Stamp adds data-anchor to the shapes of an SVG whose statements appear in
// anchors, and reports how many it matched. Shapes with no statement of their
// own — a box that exists only because an edge mentioned it — are left alone,
// and clicking them falls through to the diagram's own block, which is the
// honest answer.
func Stamp(svg string, anchors []Anchor) (string, int) {
	edges, nodes, mentions := index(anchors)
	root := ""
	if m := rootSVG.FindStringSubmatch(svg); m != nil {
		root = m[1]
	}
	matched := 0
	svg = edgePath.ReplaceAllStringFunc(svg, func(tag string) string {
		m := edgePath.FindStringSubmatch(tag)
		if m == nil {
			return tag
		}
		block, ok := edgeBlock(edges, m[1])
		if !ok {
			return tag
		}
		matched++
		return mark(tag, block) + hitPath(tag, block)
	})
	svg = edgeLabelGroup.ReplaceAllStringFunc(svg, func(tag string) string {
		m := edgeLabelGroup.FindStringSubmatch(tag)
		if m == nil {
			return tag
		}
		block, ok := edgeBlock(edges, m[1])
		if !ok {
			return tag
		}
		matched++
		return mark(tag, block)
	})
	svg = groupTag.ReplaceAllStringFunc(svg, func(tag string) string {
		name := shapeName(tag, root)
		if name == "" {
			return tag
		}
		block, ok := nodes[name]
		if !ok {
			// A box mermaid drew because an edge named it belongs to the
			// statement that named it — which is the line the reviewer means
			// when they click the box and say the label is wrong.
			if block, ok = mentions[name]; !ok {
				return tag
			}
		}
		matched++
		return mark(tag, block)
	})
	return svg, matched
}

// hitPath widens an edge's target. Mermaid draws an arrow two pixels wide; a
// review surface you have to hit two pixels wide is not one. The copy is
// invisible, catches the click along the same curve, and is left out of every
// highlight rule so it never paints.
func hitPath(tag, block string) string {
	d := dAttr.FindStringSubmatch(tag)
	if d == nil {
		return ""
	}
	return fmt.Sprintf(`<path class="mg-hit" d=%q fill="none" stroke="transparent" stroke-width="16" pointer-events="stroke" data-anchor=%q/>`, d[1], block)
}

// shapeName reads the diagram's own name for a group: a flowchart node's
// identifier, or a subgraph's, which mermaid writes as the cluster's id with
// the svg's id in front. A group that is neither has no name here.
func shapeName(tag, root string) string {
	class := classAttr.FindStringSubmatch(tag)
	id := idAttr.FindStringSubmatch(tag)
	if class == nil || id == nil {
		return ""
	}
	kind := ""
	for _, c := range strings.Fields(class[1]) {
		if c == "node" || c == "cluster" {
			kind = c
			break
		}
	}
	switch kind {
	case "node":
		if m := flowNode.FindStringSubmatch(id[1]); m != nil {
			return m[1]
		}
		return ""
	case "cluster":
		if root == "" {
			return id[1]
		}
		return strings.TrimPrefix(id[1], root+"-")
	}
	return ""
}

// index keys the anchors the way the SVG names them: an edge by the pair it
// connects, a node or subgraph by its identifier. mentions is the fallback for
// a box no statement declares on its own — the first statement that names it,
// which is where mermaid took its label from.
func index(anchors []Anchor) (edges, nodes, mentions map[string]string) {
	edges, nodes, mentions = map[string]string{}, map[string]string{}, map[string]string{}
	note := func(m map[string]string, key, block string) {
		if _, taken := m[key]; !taken {
			m[key] = block
		}
	}
	for _, a := range anchors {
		if parts := strings.Split(a.Name, "-->"); len(parts) >= 2 {
			// A chain (a-->b-->c) is one statement and draws as several
			// paths; each of its links points back at it.
			for i := 0; i+1 < len(parts); i++ {
				note(edges, parts[i]+"\x00"+parts[i+1], a.Block)
			}
			for _, part := range parts {
				note(mentions, part, a.Block)
			}
			continue
		}
		note(nodes, a.Name, a.Block)
	}
	return edges, nodes, mentions
}

// edgeBlock resolves mermaid's `L_<from>_<to>_<n>` path id against the
// statements the document wrote. Identifiers may themselves contain
// underscores, so where the pair splits is ambiguous: every split is tried and
// the one that names a statement wins. A diagram with both `a_b-->c` and
// `a-->b_c` is ambiguous in mermaid's own ids too, and there the first split
// wins — the same answer mermaid's own click handlers would give.
func edgeBlock(edges map[string]string, id string) (string, bool) {
	parts := strings.Split(id, "_")
	if len(parts) < 3 {
		return "", false
	}
	parts = parts[:len(parts)-1] // drop the edge index
	for i := 1; i < len(parts); i++ {
		key := strings.Join(parts[:i], "_") + "\x00" + strings.Join(parts[i:], "_")
		if block, ok := edges[key]; ok {
			return block, true
		}
	}
	return "", false
}

// mark adds the anchor to an opening tag, leaving whatever else it carries —
// including whether it closes itself, which mermaid's paths do. Only a data
// attribute: node groups already have a class, and a second one would be
// invalid markup that browsers resolve by ignoring ours.
func mark(tag, block string) string {
	body, close := strings.TrimSuffix(tag, ">"), ">"
	if strings.HasSuffix(body, "/") {
		body, close = strings.TrimSuffix(body, "/"), "/>"
	}
	return body + fmt.Sprintf(" data-anchor=%q", block) + close
}
