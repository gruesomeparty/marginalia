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

// ValidType reports whether t is a known event type.
func ValidType(t string) bool {
	switch t {
	case TypeComment, TypeSuggestEdit, TypeQuestion, TypeApprove, TypeReject, TypeReviewDone:
		return true
	}
	return false
}
