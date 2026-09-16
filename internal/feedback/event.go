package feedback

import (
	"crypto/sha256"
	"encoding/hex"
)

// Event is one append-only feedback record.
type Event struct {
	Doc    string `json:"doc"`
	Block  string `json:"block"`
	Quote  string `json:"quote"`
	Hash   string `json:"hash"`
	Type   string `json:"type"`
	Text   string `json:"text"`
	Author string `json:"author"`
	Ts     string `json:"ts"`
	// Fields carries the structured values a configured action collected
	// alongside the note — a severity, a category. Omitted when empty, so a
	// log written without a review config reads exactly as it always did.
	Fields map[string]string `json:"fields,omitempty"`
	// ReplyTo names the note this one answers, by NoteID. A question from a
	// human was a dead end until this existed: the agent could revise the
	// document or say nothing, and the human came back to the page to find
	// their own question still hanging there.
	//
	// It is append-only like everything else — a thread is a *reading* of the
	// log, assembled by Materialize, never a mutation of the event it
	// answers. Omitted on every event written before this, so old logs parse
	// and re-serialize unchanged.
	ReplyTo string `json:"reply_to,omitempty"`
}

// NoteID identifies an event so another event can point at it.
//
// Derived, not stored: the same tuple cmd/import.go already treats as an
// event's identity, so two events equal on it are already the same event as
// far as importing is concerned, and an id collision is by construction not a
// collision. It deliberately excludes Doc, because import rewrites that field
// — an id that changed on the way through export and back would break every
// thread it carried. And because it is derived, every log already on disk has
// ids the moment this ships; an id written at creation time would have left
// them all unreachable.
func NoteID(e Event) string {
	sum := sha256.Sum256([]byte(e.Block + "\x00" + e.Type + "\x00" + e.Text + "\x00" + e.Author + "\x00" + e.Ts))
	return hex.EncodeToString(sum[:])[:12]
}

// Feedback event types.
const (
	TypeComment     = "comment"
	TypeSuggestEdit = "suggest_edit"
	TypeQuestion    = "question"
	TypeApprove     = "approve"
	TypeReject      = "reject"
	TypeReviewDone  = "review_done"
)

// There is deliberately no ValidType here any more: which types a review
// accepts is the review configuration's business (internal/review), since an
// agent can add its own vocabulary. A second list of "the types there are"
// would be a copy waiting to drift.
