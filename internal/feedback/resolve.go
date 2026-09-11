package feedback

// Resolve returns the latest event per block (chronological by Ts).
// Events with an empty Block (e.g. review_done) are excluded.
func Resolve(events []Event) map[string]Event {
	latest := make(map[string]Event)
	for _, e := range events {
		if e.Block == "" {
			continue
		}
		if cur, ok := latest[e.Block]; !ok || e.Ts >= cur.Ts {
			latest[e.Block] = e
		}
	}
	return latest
}

// Note is one feedback event as a *reader* sees it after the document has
// moved on: the event exactly as written, plus whether the text it was
// anchored to has changed since. The event itself is never modified — the log
// is append-only and staleness is a view, not a fact on disk.
type Note struct {
	Event Event `json:"event"`
	Stale bool  `json:"stale"`
}

// State is one block's materialized review state: what the reviewer most
// recently said about it, everything they said before, and whether that still
// applies to the block as it now reads.
type State struct {
	Block    string `json:"block"`
	Current  Note   `json:"current"`
	History  []Note `json:"history"`
	Stale    bool   `json:"stale"`    // the current note predates an edit to the block
	Orphaned bool   `json:"orphaned"` // the block it was written against is gone
}

// Resolution is a feedback log materialized against a document as it reads
// now — the second-pass view: what was said, what still applies, and what the
// document has outrun.
type Resolution struct {
	States   []State `json:"states"`
	Comments int     `json:"comments"`
	Stale    int     `json:"stale"`
	Orphaned int     `json:"orphaned"`
	Done     bool    `json:"done"`
}

// Materialize replays a log against a document's current blocks. hashes maps
// each block ID to its current content hash and order lists the blocks in
// document order; a note whose block appears in neither is **orphaned** — the
// text it was written against is gone, so it is surfaced at the end rather
// than silently dropped, which is the whole point of anchoring by hash.
func Materialize(events []Event, hashes map[string]string, order []string) Resolution {
	var res Resolution
	byBlock := make(map[string][]Note, len(order))
	var seen []string // orphan blocks, in the order the log mentions them
	for _, e := range events {
		if e.Type == TypeReviewDone {
			res.Done = true
			continue
		}
		if e.Block == "" {
			continue
		}
		res.Comments++
		hash, known := hashes[e.Block]
		if !known {
			if _, ok := byBlock[e.Block]; !ok {
				seen = append(seen, e.Block)
			}
		}
		byBlock[e.Block] = append(byBlock[e.Block], Note{
			Event: e,
			// A note with no hash predates hashing, or was written against a
			// block that has none; it is reported as it is, not as stale.
			Stale: known && e.Hash != "" && e.Hash != hash,
		})
	}
	for _, block := range order {
		if notes, ok := byBlock[block]; ok {
			res.States = append(res.States, state(block, notes, false))
		}
	}
	for _, block := range seen {
		if _, known := hashes[block]; known {
			continue
		}
		res.States = append(res.States, state(block, byBlock[block], true))
	}
	for _, s := range res.States {
		if s.Stale {
			res.Stale++
		}
		if s.Orphaned {
			res.Orphaned++
		}
	}
	return res
}

// state assembles one block's view. The current note is the latest by
// timestamp, matching Resolve's rule: later events on a block override
// earlier ones when materializing.
func state(block string, notes []Note, orphaned bool) State {
	current := notes[0]
	for _, n := range notes[1:] {
		if n.Event.Ts >= current.Event.Ts {
			current = n
		}
	}
	return State{
		Block:    block,
		Current:  current,
		History:  notes,
		Stale:    current.Stale,
		Orphaned: orphaned,
	}
}

// Suggestion is a suggest_edit as an applier sees it: which block, the text it
// was written against, and the replacement.
type Suggestion struct {
	Block       string `json:"block"`
	Quote       string `json:"quote"`
	Hash        string `json:"hash"`
	Replacement string `json:"replacement"`
	Author      string `json:"author"`
	Ts          string `json:"ts"`
	// Reason is why a suggestion needs confirmation before it is applied;
	// empty for an applicable one.
	Reason string `json:"reason,omitempty"`
}

// Reasons a suggestion cannot be applied unread.
const (
	ReasonStale      = "the block changed after this was written"
	ReasonNoHash     = "written without a hash, so the match cannot be proven"
	ReasonOrphaned   = "the block no longer exists in the document"
	ReasonSuperseded = "a later note on this block supersedes it"
)

// Suggestions splits the log's suggest_edit notes into the ones that may be
// applied verbatim and the ones a human has to confirm first.
//
// Applying requires all three of: the suggestion is the note that stands for
// its block, its hash matches the block as the document now reads, and it
// carries a hash at all. Materialize treats a hash-less note as not-stale
// rather than suspect, which is right for reading but not for editing — "cannot
// be proven to match" is not good enough to change a document unread.
//
// Nothing here mutates anything: the caller owns the document, and the source
// document is never written by Marginalia.
func (r Resolution) Suggestions() (applicable, needsConfirmation []Suggestion) {
	for _, st := range r.States {
		for _, note := range st.History {
			if note.Event.Type != TypeSuggestEdit || note.Event.Text == "" {
				continue
			}
			s := Suggestion{
				Block:       st.Block,
				Quote:       note.Event.Quote,
				Hash:        note.Event.Hash,
				Replacement: note.Event.Text,
				Author:      note.Event.Author,
				Ts:          note.Event.Ts,
			}
			switch {
			case st.Orphaned:
				s.Reason = ReasonOrphaned
			case note.Event.Ts != st.Current.Event.Ts || st.Current.Event.Type != TypeSuggestEdit:
				s.Reason = ReasonSuperseded
			case note.Stale:
				s.Reason = ReasonStale
			case note.Event.Hash == "":
				s.Reason = ReasonNoHash
			}
			if s.Reason == "" {
				applicable = append(applicable, s)
			} else {
				needsConfirmation = append(needsConfirmation, s)
			}
		}
	}
	return applicable, needsConfirmation
}
