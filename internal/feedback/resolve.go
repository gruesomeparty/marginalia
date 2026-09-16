package feedback

import "sort"

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
	// ID is what a reply points at. Derived from the event, never stored.
	ID string `json:"id"`
	// Replies are the answers to this note, oldest first. A thread is one
	// level deep on purpose: a reply to a reply re-roots to the note that
	// started it, because a review is a conversation about a block, not a
	// forum. Replies are a Reply and not a Note for exactly that reason —
	// the type cannot express a thread the design does not have.
	Replies []Reply `json:"replies,omitempty"`
	// Progress is the addressed/confirm/reopen events on this note, oldest
	// first — where it stands in the revision loop, as opposed to what was
	// said about it. Same shape as a reply because it is the same kind of
	// thing: an event that names this note.
	Progress []Reply `json:"progress,omitempty"`
	// Status is where the note stands: outstanding until something says
	// otherwise, then whatever the latest progress event made it.
	//
	// Deliberately orthogonal to Stale. Stale says the *text* moved; Status
	// says whether anyone claimed to act on the note. Conflating them is the
	// bug this exists to fix: before it, a block edited in answer to a note
	// and a block edited for unrelated reasons looked identical, so a second
	// round meant re-reading the whole document.
	Status string `json:"status"`
	// Unclaimed marks an `addressed` the document does not bear out: the
	// block still hashes to what it did before the claimed edit, so nothing
	// changed. The claim is shown either way — this is a review tool, not a
	// court — but it is shown as unsubstantiated.
	Unclaimed bool `json:"unclaimed,omitempty"`
	// Dangling marks a reply whose target is not in this log — a partial
	// import, or a hand-edited file. It is surfaced as a note of its own
	// rather than dropped, for the same reason an orphan is.
	Dangling bool `json:"dangling,omitempty"`
}

// Where a note stands in the revision loop.
const (
	StatusOutstanding = "outstanding"
	StatusAddressed   = "addressed"
	StatusConfirmed   = "confirmed"
	StatusReopened    = "reopened"
)

// statusAfter maps a progress event's type to the status it produces. A note
// nobody has touched is outstanding: silence is not resolution.
func statusAfter(typ string) string {
	switch typ {
	case TypeAddressed:
		return StatusAddressed
	case TypeConfirm:
		return StatusConfirmed
	case TypeReopen:
		return StatusReopened
	}
	return StatusOutstanding
}

// Reply is one answer to a note. It is deliberately not a Note: a thread is
// one level deep, and a type that could nest would invite a forum.
type Reply struct {
	Event Event  `json:"event"`
	Stale bool   `json:"stale"`
	ID    string `json:"id"`
}

// note promotes a reply whose target is nowhere in the log back to a note of
// its own, so it is read rather than dropped.
func (r Reply) note() Note {
	return Note{Event: r.Event, Stale: r.Stale, ID: r.ID, Dangling: true}
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
	// Replies counts answers, separately from comments: the header count is
	// how much the *reviewer* said, and it must not inflate because the agent
	// answered them.
	Replies int `json:"replies"`
	// Outstanding is how many notes still ask for something: nobody has
	// claimed to address them and nobody has settled them. It is the length
	// of the second round's queue, which is the number that makes a re-review
	// short instead of a full re-read.
	Outstanding int `json:"outstanding"`
	// Addressed counts notes the agent says it acted on and the reviewer has
	// not yet ruled on — the "is this right now?" queue.
	Addressed int  `json:"addressed"`
	Confirmed int  `json:"confirmed"`
	Reopened  int  `json:"reopened"`
	Stale     int  `json:"stale"`
	Orphaned  int  `json:"orphaned"`
	Done      bool `json:"done"`
}

// Materialize replays a log against a document's current blocks. hashes maps
// each block ID to its current content hash and order lists the blocks in
// document order; a note whose block appears in neither is **orphaned** — the
// text it was written against is gone, so it is surfaced at the end rather
// than silently dropped, which is the whole point of anchoring by hash.
func Materialize(events []Event, hashes map[string]string, order []string) Resolution {
	var res Resolution
	byBlock := make(map[string][]Note, len(order))
	replies := map[string][]Reply{}
	var seen []string // orphan blocks, in the order the log mentions them
	for _, e := range events {
		if e.Type == TypeReviewDone {
			res.Done = true
			continue
		}
		if e.Block == "" {
			continue
		}
		hash, known := hashes[e.Block]
		if !known {
			if _, ok := byBlock[e.Block]; !ok {
				seen = append(seen, e.Block)
			}
		}
		note := Note{
			Event: e,
			ID:    NoteID(e),
			// A note with no hash predates hashing, or was written against a
			// block that has none; it is reported as it is, not as stale.
			Stale: known && e.Hash != "" && e.Hash != hash,
		}
		if e.ReplyTo != "" {
			// An answer and a status both name another note, so both travel
			// the same road here and are split apart when they are attached.
			// Only an answer counts as something said.
			if e.Type == TypeReply {
				res.Replies++
			}
			replies[e.ReplyTo] = append(replies[e.ReplyTo], Reply{Event: e, Stale: note.Stale, ID: note.ID})
			continue
		}
		res.Comments++
		byBlock[e.Block] = append(byBlock[e.Block], note)
	}
	// Hang each reply under the note it answers, re-rooting a reply to a
	// reply. A reply naming nothing in this log becomes a note of its own,
	// marked dangling.
	seen = attachReplies(byBlock, replies, seen, hashes)
	// Status is derived after attaching, because it is a reading of the
	// progress events — never a field anyone wrote.
	for block := range byBlock {
		settle(byBlock[block], hashes)
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
		for _, n := range s.History {
			switch n.Status {
			case StatusAddressed:
				res.Addressed++
			case StatusConfirmed:
				res.Confirmed++
			case StatusReopened:
				res.Reopened++
				res.Outstanding++
			default:
				// An approve asks for nothing, so counting it as outstanding
				// would make a fully-approved document read as a full queue.
				// Every other note is outstanding until something says
				// otherwise — a configured `nit` included, because the agent
				// asked for that word and has to answer for it.
				if n.Event.Type != TypeApprove && !n.Dangling {
					res.Outstanding++
				}
			}
		}
	}
	return res
}

// attachReplies hangs each reply under the note it answers.
//
// Threads are one level deep: a reply to a reply re-roots to the note that
// started the thread, because a review is a conversation about a block, not a
// forum. The walk is bounded, so a hand-edited file that makes a cycle stops
// rather than spinning.
func attachReplies(byBlock map[string][]Note, replies map[string][]Reply, seen []string, hashes map[string]string) []string {
	if len(replies) == 0 {
		return seen
	}
	// Where every root note lives, so a reply can find it.
	type place struct{ block string }
	at := map[string]place{}
	for block, notes := range byBlock {
		for _, n := range notes {
			at[n.ID] = place{block: block}
		}
	}
	// A reply may name another reply; resolve to the root it belongs to.
	root := map[string]string{}
	var resolve func(id string, depth int) (string, bool)
	resolve = func(id string, depth int) (string, bool) {
		if depth > 16 {
			return "", false
		}
		if _, ok := at[id]; ok {
			return id, true
		}
		for target, group := range replies {
			for _, r := range group {
				if r.ID == id {
					return resolve(target, depth+1)
				}
			}
		}
		return "", false
	}
	for target := range replies {
		if r, ok := resolve(target, 0); ok {
			root[target] = r
		}
	}
	for target, group := range replies {
		rootID, ok := root[target]
		if !ok {
			// Nothing in this log answers to that id. Surface the reply as a
			// note of its own rather than dropping it — the same reason an
			// orphan is listed instead of discarded.
			for _, r := range group {
				n := r.note()
				if _, known := hashes[n.Event.Block]; !known {
					if _, listed := byBlock[n.Event.Block]; !listed {
						seen = append(seen, n.Event.Block)
					}
				}
				byBlock[n.Event.Block] = append(byBlock[n.Event.Block], n)
			}
			continue
		}
		block := at[rootID].block
		notes := byBlock[block]
		for i := range notes {
			if notes[i].ID != rootID {
				continue
			}
			// An answer goes in the thread; a status goes in the revision
			// loop. They arrive mixed because both name this note.
			for _, r := range group {
				if r.Event.Type == TypeReply {
					notes[i].Replies = append(notes[i].Replies, r)
					continue
				}
				notes[i].Progress = append(notes[i].Progress, r)
			}
			byTime(notes[i].Replies)
			byTime(notes[i].Progress)
		}
		byBlock[block] = notes
	}
	return seen
}

// byTime orders events oldest first, which is how a conversation and a
// revision loop both read.
func byTime(rs []Reply) {
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].Event.Ts < rs[b].Event.Ts })
}

// settle derives where each note stands from its progress events, and checks
// the one claim that can be checked.
//
// An `addressed` says "I edited this block; before my edit it hashed X". If
// the block *still* hashes to X, nothing changed and the claim is not borne
// out by the document. That is reported, not suppressed: the agent may have
// edited a different block, or meant to and did not, and either way the
// reviewer should see the claim next to the fact that the text did not move.
func settle(notes []Note, hashes map[string]string) {
	for i := range notes {
		n := &notes[i]
		n.Status = StatusOutstanding
		if len(n.Progress) == 0 {
			continue
		}
		last := n.Progress[len(n.Progress)-1]
		n.Status = statusAfter(last.Event.Type)
		if last.Event.Type != TypeAddressed {
			continue
		}
		// A claim with no hash cannot be checked — same rule Suggestions()
		// applies: "cannot be proven" is not the same as "false".
		if now, known := hashes[n.Event.Block]; known && last.Event.Hash != "" && now == last.Event.Hash {
			n.Unclaimed = true
		}
	}
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
