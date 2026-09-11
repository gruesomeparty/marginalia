package feedback

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
