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
