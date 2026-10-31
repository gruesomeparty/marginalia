package diagram

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Sequence, state and ER drawings name their parts differently from a
// flowchart, and less completely (issue #153). What mermaid 12 writes:
//
//	<g data-et="participant" data-id="A">                 a participant
//	<line data-et="message" data-from="C" data-to="A">    a message
//	<g data-et="note">, <g data-et="control-structure">   a note, a loop/alt…
//	<g class="node …" id="…-state-Paid-2">                a state
//	<path data-et="edge" data-id="edge1">                 a transition, by ordinal
//	<g class="node …" id="…-entity-ORDER-1">              an entity
//	<path data-id="id_entity-A-0_entity-B-1_0">           a relationship
//
// A message, note or group carries no name of its own, so the k-th one drawn
// answers for the k-th one written — and only when the drawing holds exactly
// as many as the source, since a count that disagrees means the pairing would
// guess. A transition is matched the same way, and not at all once a
// composite state is involved, whose edges mermaid numbers in an order of its
// own. What cannot be matched stays unanchored: a click there lands on the
// diagram, and the statement is one toggle away in the source.

var (
	// Quote-aware, since an anchor such as `C->A` puts a `>` in a value.
	shapeTag  = regexp.MustCompile(`(?is)<(?:g|path|line)\b(?:[^>"]|"[^"]*")*>`)
	stateNode = regexp.MustCompile(`(?i)-state-(.+)-\d+$`)
	erNode    = regexp.MustCompile(`(?i)-entity-(.+)-\d+$`)
	erEdge    = regexp.MustCompile(`^id_entity-(.+?)-\d+_entity-(.+)-\d+_\d+$`)
	edgeOrd   = regexp.MustCompile(`^edge(\d+)$`)
)

// attrRe holds one compiled pattern per attribute name attr is asked for.
var attrRe = map[string]*regexp.Regexp{}

func init() {
	for _, n := range []string{"id", "class", "data-et", "data-id", "data-from", "data-to", "data-anchor", "x1", "y1", "x2", "y2"} {
		attrRe[n] = regexp.MustCompile(`\s` + regexp.QuoteMeta(n) + `="([^"]*)"`)
	}
}

// attr reads one attribute of an opening tag.
func attr(tag, name string) string {
	m := attrRe[name].FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return m[1]
}

// base is a statement's name without the number a repeat was given.
func base(name string) string {
	if i := strings.LastIndex(name, "#"); i > 0 {
		return name[:i]
	}
	return name
}

// kindIndex groups the statements the other diagram types are matched on.
type kindIndex struct {
	byKind  map[string][]Anchor // statements of a kind, in source order
	byName  map[string][]Anchor // messages and relationships by base name
	named   map[string]string   // participants, states, entities by name
	mention map[string]string   // a name to the first statement naming it
	nested  bool                // a transition sits inside a composite state
	present map[string]bool     // which kinds the document has
	seen    map[string]int      // how many of each have been drawn so far
	drawn   map[string]int      // how many of each the drawing holds
}

func newKindIndex(anchors []Anchor) *kindIndex {
	k := &kindIndex{
		byKind: map[string][]Anchor{}, byName: map[string][]Anchor{},
		named: map[string]string{}, mention: map[string]string{},
		present: map[string]bool{}, seen: map[string]int{}, drawn: map[string]int{},
	}
	for _, a := range anchors {
		k.present[a.Kind] = true
		k.byKind[a.Kind] = append(k.byKind[a.Kind], a)
		var ends []string
		switch a.Kind {
		case "message":
			ends = strings.SplitN(base(a.Name), "->", 2)
			k.byName[base(a.Name)] = append(k.byName[base(a.Name)], a)
		case "relationship":
			ends = strings.SplitN(base(a.Name), "--", 2)
			k.byName[base(a.Name)] = append(k.byName[base(a.Name)], a)
		case "transition":
			ends = strings.SplitN(base(a.Name), "-->", 2)
			k.nested = k.nested || a.Nested
		case "participant", "state", "entity":
			if _, ok := k.named[base(a.Name)]; !ok {
				k.named[base(a.Name)] = a.Block
			}
		}
		for _, e := range ends {
			if _, ok := k.mention[e]; !ok {
				k.mention[e] = a.Block
			}
		}
	}
	// mermaid draws a loop or alt when it reaches its `end`, so an inner
	// group is drawn before the one around it: pair them in closing order.
	k.byKind["group"] = closingOrder(k.byKind["group"])
	return k
}

// closingOrder turns groups listed as the source opens them into the order
// they close in: each after everything nested in it, siblings in turn.
func closingOrder(open []Anchor) []Anchor {
	out := make([]Anchor, 0, len(open))
	var stack []Anchor
	for _, g := range open {
		for len(stack) > 0 && !strings.HasPrefix(g.Block, stack[len(stack)-1].Block+"/") {
			out = append(out, stack[len(stack)-1])
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, g)
	}
	for i := len(stack) - 1; i >= 0; i-- {
		out = append(out, stack[i])
	}
	return out
}

// stampKinds anchors the shapes of a sequence, state or ER drawing.
func stampKinds(svg string, anchors []Anchor) (string, int) {
	k := newKindIndex(anchors)
	if !k.present["message"] && !k.present["participant"] && !k.present["transition"] &&
		!k.present["state"] && !k.present["relationship"] && !k.present["entity"] {
		return svg, 0
	}
	for _, tag := range shapeTag.FindAllString(svg, -1) {
		if key := k.ordinalKey(tag); key != "" {
			k.drawn[key]++
		}
	}
	matched := 0
	svg = shapeTag.ReplaceAllStringFunc(svg, func(tag string) string {
		if strings.Contains(tag, "data-anchor=") {
			return tag
		}
		block, hit := k.match(tag)
		if block == "" {
			return tag
		}
		matched++
		switch {
		case hit && strings.HasPrefix(tag, "<line"):
			return mark(tag, block) + hitLine(tag, block)
		case hit:
			return mark(tag, block) + hitPath(tag, block)
		}
		return mark(tag, block)
	})
	return svg, matched
}

// ordinalKey names the sequence a tag is counted in, for the shapes matched
// by position: "message C->A", "note", "group", "transition", or the
// relationship pair. A label shares its edge's key but is not counted.
func (k *kindIndex) ordinalKey(tag string) string {
	switch et := attr(tag, "data-et"); {
	case et == "message":
		return "message " + attr(tag, "data-from") + "->" + attr(tag, "data-to")
	case et == "note":
		return "note"
	case et == "control-structure":
		return "group"
	case et == "edge" && edgeOrd.MatchString(attr(tag, "data-id")):
		return "transition"
	case et == "edge":
		if m := erEdge.FindStringSubmatch(attr(tag, "data-id")); m != nil {
			return "relationship " + m[1] + "--" + m[2]
		}
	}
	return ""
}

// match finds the statement a tag draws, and whether it is a line or path
// thin enough to need a wider hit target.
func (k *kindIndex) match(tag string) (block string, hit bool) {
	if key := k.ordinalKey(tag); key != "" {
		return k.nth(key), strings.HasPrefix(tag, "<line") || strings.HasPrefix(tag, "<path")
	}
	// An edge's label is a group carrying the edge's data-id: it answers for
	// the same statement, at the ordinal its edge already took.
	if id := attr(tag, "data-id"); strings.HasPrefix(tag, "<g") && attr(tag, "data-et") == "" {
		if m := edgeOrd.FindStringSubmatch(id); m != nil && k.present["transition"] && !k.nested {
			n, _ := strconv.Atoi(m[1])
			if ts := k.byKind["transition"]; n < len(ts) && k.drawn["transition"] == len(ts) {
				return ts[n].Block, false
			}
		}
		if m := erEdge.FindStringSubmatch(id); m != nil {
			// The k-th label of a pair answers for its k-th relationship, on
			// the same terms as the edges: only when the counts agree.
			pair := m[1] + "--" + m[2]
			i := k.seen["label "+pair]
			k.seen["label "+pair]++
			if rs := k.byName[pair]; i < len(rs) && k.drawn["relationship "+pair] == len(rs) {
				return rs[i].Block, false
			}
		}
	}
	if attr(tag, "data-et") == "participant" {
		return k.name(attr(tag, "data-id")), false
	}
	if !strings.Contains(" "+attr(tag, "class")+" ", " node ") {
		return "", false
	}
	id := attr(tag, "id")
	if m := stateNode.FindStringSubmatch(id); m != nil && (k.present["transition"] || k.present["state"]) {
		return k.name(m[1]), false
	}
	if m := erNode.FindStringSubmatch(id); m != nil && (k.present["relationship"] || k.present["entity"]) {
		return k.name(m[1]), false
	}
	return "", false
}

// nth takes the next statement of an ordinal sequence, if the drawing and
// the source agree on how many there are.
func (k *kindIndex) nth(key string) string {
	i := k.seen[key]
	k.seen[key]++
	var list []Anchor
	switch kind, name, _ := strings.Cut(key, " "); kind {
	case "message", "relationship":
		list = k.byName[name]
	case "note":
		list = k.byKind["note"]
	case "group":
		list = k.byKind["group"]
	case "transition":
		if k.nested {
			return ""
		}
		list = k.byKind["transition"]
	}
	if i >= len(list) || k.drawn[key] != len(list) {
		return ""
	}
	return list[i].Block
}

// name resolves a participant, state or entity: its own declaration, else
// the first statement that names it, which is where mermaid took it from.
func (k *kindIndex) name(n string) string {
	if b, ok := k.named[n]; ok {
		return b
	}
	return k.mention[n]
}

// hitLine is hitPath for a straight line: an invisible wide copy that takes
// the click a one-pixel message line could not.
func hitLine(tag, block string) string {
	var b strings.Builder
	b.WriteString(`<line class="mg-hit"`)
	for _, a := range []string{"x1", "y1", "x2", "y2"} {
		fmt.Fprintf(&b, " %s=%q", a, attr(tag, a))
	}
	fmt.Fprintf(&b, ` stroke="transparent" stroke-width="16" pointer-events="stroke" data-anchor=%s/>`, quoteAttr(block))
	return b.String()
}
