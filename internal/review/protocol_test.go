package review

import (
	"strings"
	"testing"
)

// The protocol types are the review's own machinery: a config cannot define
// them, and Validate must not accept them as vocabulary, or the same word
// would mean two things in one log.
func TestProtocolTypesAreNotVocabulary(t *testing.T) {
	for _, typ := range Protocol() {
		if !IsProtocol(typ) {
			t.Errorf("%q is listed as protocol but not recognised as one", typ)
		}
		if err := Default().Validate(typ, "something", nil); err == nil {
			t.Errorf("Validate accepted the protocol type %q as review vocabulary", typ)
		}
	}
	for _, typ := range []string{"comment", "approve", "blocker", ""} {
		if IsProtocol(typ) {
			t.Errorf("%q is not protocol", typ)
		}
	}
}

// A reply has to say something and has to name what it answers. The caller
// checks that the name resolves — only it has the log — but an answer with no
// words, or no note, is refusable without one.
func TestValidateProtocol(t *testing.T) {
	if err := ValidateProtocol(TypeReviewDone, "", ""); err != nil {
		t.Errorf("review_done carries nothing and needs nothing: %v", err)
	}
	if err := ValidateProtocol(TypeReply, "Because the API caps it.", "abc123"); err != nil {
		t.Errorf("a complete reply should pass: %v", err)
	}
	for _, tc := range []struct{ name, text, to, want string }{
		{"no words", "   ", "abc123", "something to say"},
		{"no note", "Because.", "  ", "reply_to"},
	} {
		err := ValidateProtocol(TypeReply, tc.text, tc.to)
		if err == nil {
			t.Errorf("%s: should have been refused", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error should say why, got %q", tc.name, err)
		}
	}
	if err := ValidateProtocol("comment", "hi", ""); err == nil {
		t.Error("ValidateProtocol is not a second door into the vocabulary")
	}
}
