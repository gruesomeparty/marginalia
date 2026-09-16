package feedback

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// Sub anchors a note to a run of text *inside* its block — a sentence in a
// paragraph, rather than the paragraph.
//
// It is a **new field, never a new block id.** A sub-block id that looked like
// a block id (`5.3/2#1`, say) would be accepted by every consumer that already
// reads `block`, and would silently mean something else to each of them. An
// event with a sub-anchor still names its block exactly as before, so
// everything written against the old schema keeps working and an old reader
// simply sees a note on the paragraph — which is where it was, just less
// precisely.
//
// The selectors are the ones the W3C annotation model settled on for the same
// problem, and for the same reason: quote plus context survives edits
// elsewhere in the block, while a bare offset does not survive anything.
type Sub struct {
	// Quote is the selected text, whitespace-normalized. This is the anchor;
	// everything else only disambiguates it.
	Quote string `json:"quote"`
	// Prefix and Suffix are the normalized text immediately around it, so two
	// identical sentences in one paragraph stay distinguishable.
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
	// Start is the rune offset the quote had when the note was written. A
	// hint, never a source of truth: it is consulted only to break a tie
	// between candidates that match equally well.
	Start int `json:"start"`
	// Hash is sha256(Quote)[:12], so a reader can tell two sub-anchors apart
	// without carrying the whole quote around.
	Hash string `json:"hash,omitempty"`
}

// SubHash is the identity of a quoted run.
func SubHash(quote string) string {
	sum := sha256.Sum256([]byte(quote))
	return hex.EncodeToString(sum[:])[:12]
}

// NormalizeSpace collapses runs of whitespace, the same way the document
// package does before hashing a block. The page and the server have to agree
// on this or a quote written by one would never be found by the other.
func NormalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// context is how much text on either side of a quote is kept. Long enough to
// separate two similar sentences, short enough that editing a neighbouring
// sentence does not move the anchor.
const context = 32

// Located is where a sub-anchor resolved in the block as it now reads.
type Located struct {
	Start int `json:"start"` // rune offset into the block's normalized text
	End   int `json:"end"`
}

// Locate finds a sub-anchor's quote in the block's current text.
//
// Returns ok=false when the quote is not there any more, which is what makes a
// sub-anchored note honestly **stale** instead of silently re-anchoring to
// whatever happens to sit at the old offset. That is the failure this design
// exists to avoid: an offset alone would always "find" something.
//
// When the quote appears more than once, the candidates are scored by how much
// of the recorded context still surrounds them, and ties go to the one nearest
// the offset the note was written at.
func Locate(sub *Sub, text string) (Located, bool) {
	if sub == nil || sub.Quote == "" {
		return Located{}, false
	}
	hay := NormalizeSpace(text)
	needle := NormalizeSpace(sub.Quote)
	if needle == "" {
		return Located{}, false
	}
	// Byte offsets of every occurrence.
	var at []int
	for i := 0; ; {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			break
		}
		at = append(at, i+j)
		i += j + 1
		if i > len(hay) {
			break
		}
	}
	if len(at) == 0 {
		return Located{}, false
	}
	best, bestScore := -1, -1
	for _, pos := range at {
		score := overlap(hay[:pos], sub.Prefix, true) + overlap(hay[pos+len(needle):], sub.Suffix, false)
		start := utf8.RuneCountInString(hay[:pos])
		switch {
		case score > bestScore:
			best, bestScore = pos, score
		case score == bestScore && best >= 0:
			// Equally well anchored: the one that moved least wins.
			if abs(start-sub.Start) < abs(utf8.RuneCountInString(hay[:best])-sub.Start) {
				best = pos
			}
		}
	}
	start := utf8.RuneCountInString(hay[:best])
	return Located{Start: start, End: start + utf8.RuneCountInString(needle)}, true
}

// overlap counts how many characters of the recorded context still sit against
// the candidate — from the inside out, since it is the text touching the quote
// that identifies it. before=true compares the tail of side against the tail of
// want; false compares the heads.
func overlap(side, want string, before bool) int {
	if want == "" {
		return 0
	}
	s, w := []rune(side), []rune(want)
	n := 0
	for n < len(s) && n < len(w) {
		var a, b rune
		if before {
			a, b = s[len(s)-1-n], w[len(w)-1-n]
		} else {
			a, b = s[n], w[n]
		}
		if a != b {
			break
		}
		n++
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// MakeSub builds a sub-anchor for a quote inside a block's text, or nil when
// the quote is not in it. Callers hand it the text the *server* will re-locate
// against, so the page and the server cannot disagree about what was selected.
func MakeSub(text, quote string) *Sub {
	hay, needle := NormalizeSpace(text), NormalizeSpace(quote)
	if needle == "" || hay == "" {
		return nil
	}
	pos := strings.Index(hay, needle)
	if pos < 0 {
		return nil
	}
	return &Sub{
		Quote:  needle,
		Prefix: tail(hay[:pos], context),
		Suffix: head(hay[pos+len(needle):], context),
		Start:  utf8.RuneCountInString(hay[:pos]),
		Hash:   SubHash(needle),
	}
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
