package mermaid

import (
	"regexp"
	"strings"
)

// Statement kinds of the sequence, state and ER diagrams.
const (
	KindParticipant  = "participant"  // `participant A as Alice`, `actor U`
	KindMessage      = "message"      // `A->>B: text`
	KindNote         = "note"         // `Note right of A: text`
	KindGroup        = "group"        // `loop …` / `alt …` … `end`
	KindBranch       = "branch"       // `else …`, `and …` inside a group
	KindState        = "state"        // `Pending`, `state Paid { … }`
	KindTransition   = "transition"   // `Pending --> Paid : charge`
	KindEntity       = "entity"       // `CUSTOMER { … }`
	KindAttribute    = "attribute"    // `string name PK` inside an entity
	KindRelationship = "relationship" // `CUSTOMER ||--o{ ORDER : places`
)

func (p *parser) flowchart() bool { return p.dia.Type == "flowchart" || p.dia.Type == "graph" }

// other files one line of a sequence, state or ER diagram.
func (p *parser) other(sp span, line int) {
	s := &Stmt{Text: sp.text, Line: line, Start: sp.start, End: sp.end}
	switch p.dia.Type {
	case "sequence":
		p.sequence(s)
	case "state":
		p.state(s)
	default:
		p.er(s)
	}
}

// open files s as a container whose statements follow until it closes.
func (p *parser) open(s *Stmt) {
	p.add(s)
	p.stack = append(p.stack, s)
}

// close ends the innermost container, flushing a comment stranded at its
// end. A closer with nothing open is kept as a statement of its own.
func (p *parser) close(s *Stmt) {
	p.flush()
	if n := len(p.stack); n > 0 {
		p.stack = p.stack[:n-1]
		return
	}
	s.Kind, s.Name = KindStatement, s.Text
	p.add(s)
}

var (
	// A message's arrow: solid or dotted, with an arrowhead, a cross, an
	// open end, or the two-way head; activation marks follow it.
	seqArrow = regexp.MustCompile(`<<-{1,2}>>|-{1,2}(?:>>|>|\))`)
	// The cross head is tried only when no other arrow is there, since `-x`
	// also spells the middle of a name like `web-xray`.
	seqCross = regexp.MustCompile(`-{1,2}x`)
	// Group keywords open a block that `end` closes; branches split one.
	seqGroups     = words("loop alt opt par par_over critical break rect box")
	seqBranches   = words("else and option")
	seqDirectives = map[string]string{
		"activate": "activate", "deactivate": "deactivate", "autonumber": "autonumber",
		"title": "title", "accTitle": "acc_title", "accDescr": "acc_descr", "destroy": "destroy",
		"link": "link", "links": "link", "properties": "properties", "details": "details",
	}
)

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(s) {
		out[w] = true
	}
	return out
}

// sequence classifies a statement of a sequence diagram. A message is named
// by who sends it to whom (`C->A`); repeats of the same pair are numbered by
// whoever assigns the ids, which makes a message's anchor its pair and its
// ordinal among that pair's messages.
func (p *parser) sequence(s *Stmt) {
	kw, rest := keyword(s.Text)
	lower := strings.ToLower(kw)
	if lower == "create" {
		kw, rest = keyword(rest)
		lower = strings.ToLower(kw)
	}
	switch {
	case lower == "end":
		p.close(s)
		return
	case seqGroups[lower]:
		s.Kind, s.Name = KindGroup, lower
		p.open(s)
		return
	case seqBranches[lower]:
		s.Kind, s.Name = KindBranch, lower
	case lower == "participant" || lower == "actor":
		s.Kind, s.Name = KindParticipant, or(pathToken(rest), lower)
	case lower == "note":
		s.Kind, s.Name = KindNote, "note"
	case seqDirectives[kw] != "":
		s.Kind, s.Name = seqDirectives[kw], seqDirectives[kw]
		if t := pathToken(rest); t != "" && kw != "title" && !strings.HasPrefix(kw, "acc") {
			s.Name += "/" + t
		}
	default:
		head := s.Text
		if i := strings.Index(head, ":"); i >= 0 {
			head = head[:i]
		}
		m := seqArrow.FindStringIndex(head)
		if m == nil {
			m = seqCross.FindStringIndex(head)
		}
		if m != nil {
			from := strings.TrimSpace(head[:m[0]])
			to := strings.TrimLeft(strings.TrimSpace(head[m[1]:]), "+-")
			if from != "" && to != "" {
				s.Kind, s.Name = KindMessage, from+"->"+strings.TrimSpace(to)
				break
			}
		}
		s.Kind, s.Name = KindStatement, "statement"
	}
	p.add(s)
}

// blockNote reports a state diagram note that runs to `end note`, as
// opposed to one finished on its own line after a colon.
func blockNote(body string) bool {
	kw, _ := keyword(body)
	return strings.EqualFold(kw, "note") && !strings.Contains(body, ":")
}

// state classifies a statement of a state diagram. A transition is named by
// what it connects (`Pending-->Paid`, `[*]-->Pending`), the way a flowchart
// edge is; a composite state's body hangs under its name.
func (p *parser) state(s *Stmt) {
	kw, rest := keyword(s.Text)
	switch {
	case s.Text == "}":
		p.close(s)
		return
	case s.Text == "--":
		s.Kind, s.Name = "concurrency", "--"
	case strings.EqualFold(kw, "note"):
		s.Kind, s.Name = KindNote, "note"
	case directives[kw] != "":
		s.Kind, s.Name = directives[kw], directives[kw]
		if t := pathToken(rest); t != "" {
			s.Name += "/" + t
		}
	case strings.Contains(s.Text, "-->"):
		head := s.Text
		if i := strings.Index(head, ":"); i >= 0 {
			head = head[:i]
		}
		from, to, _ := strings.Cut(head, "-->")
		s.Kind, s.Name = KindTransition, stateName(from)+"-->"+stateName(to)
	case strings.EqualFold(kw, "state"):
		// `state "Long name" as X`, `state X <<fork>>`, `state X {`.
		name := rest
		if _, alias, ok := strings.Cut(rest, " as "); ok {
			name = alias
		}
		s.Kind, s.Name = KindState, or(stateName(strings.TrimSuffix(strings.TrimSpace(name), "{")), "state")
		if strings.HasSuffix(s.Text, "{") {
			p.open(s)
			return
		}
	default:
		head, _, _ := strings.Cut(s.Text, ":")
		s.Kind, s.Name = KindState, or(stateName(head), "state")
	}
	p.add(s)
}

// stateName is a state as a transition or declaration names it, up to any
// stereotype or description: `[*]`, `Pending`.
func stateName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t<{"); i >= 0 {
		s = s[:i]
	}
	return s
}

// er classifies a statement of an ER diagram. A relationship is named by
// the pair of entities it relates (`CUSTOMER--ORDER`): its cardinality and
// label are what it says about them, so they are hashed, not part of the
// name. An entity's attributes hang under it.
func (p *parser) er(s *Stmt) {
	kw, rest := keyword(s.Text)
	inEntity := len(p.stack) > 0
	switch {
	case s.Text == "}":
		p.close(s)
		return
	case inEntity:
		// `type name [PK|FK|UK] ["comment"]`
		s.Kind, s.Name = KindAttribute, or(pathToken(rest), "attribute")
	case directives[kw] != "":
		s.Kind, s.Name = directives[kw], directives[kw]
		if t := pathToken(rest); t != "" {
			s.Name += "/" + t
		}
	case strings.HasSuffix(s.Text, "{"):
		s.Kind, s.Name = KindEntity, or(pathToken(strings.TrimSuffix(s.Text, "{")), "entity")
		p.open(s)
		return
	default:
		head, _, labelled := strings.Cut(s.Text, ":")
		if f := strings.Fields(head); labelled && len(f) >= 3 {
			s.Kind, s.Name = KindRelationship, pathToken(f[0])+"--"+pathToken(f[len(f)-1])
			break
		}
		s.Kind, s.Name = KindEntity, or(pathToken(s.Text), "entity")
	}
	p.add(s)
}
