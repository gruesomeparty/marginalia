package feedback

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/gruesomeparty/marginalia/internal/review"
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
	// Sub anchors the note to a run of text inside the block — a sentence in
	// a paragraph. Absent on every event written before it existed, and on
	// every note about a whole block, which is still the common case. See
	// subanchor.go for why this is a field and not a longer block id.
	Sub *Sub `json:"sub,omitempty"`
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
)

// The protocol's own types, aliased from internal/review rather than spelled
// again here. Replaying a log has to recognise them, but `review` is the
// authority on which types exist, and `review_done` used to be written out in
// both places — two spellings of one word, waiting to drift. A const alias
// cannot.
const (
	TypeReviewDone = review.TypeReviewDone
	TypeReply      = review.TypeReply
	TypeAddressed  = review.TypeAddressed
	TypeConfirm    = review.TypeConfirm
	TypeReopen     = review.TypeReopen
)

// There is deliberately no ValidType here any more: which types a review
// accepts is the review configuration's business (internal/review), since an
// agent can add its own vocabulary. A second list of "the types there are"
// would be a copy waiting to drift.
