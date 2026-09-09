package protoschema

import "strings"

type tokKind int

const (
	tokIdent tokKind = iota // identifiers, keywords, numbers, qualified names
	tokString
	tokPunct
)

// token is one lexed proto token plus the comments attached to it.
type token struct {
	kind  tokKind
	text  string
	start int // byte offset of the token in the source
	end   int // byte offset just past the token
	line  int
	lead  string // comments on their own line(s) directly above this token
	trail string // comment following this token on the same line
}

// blockCommentEnd reports whether src[i:] opens the `*/` closing a block
// comment.
func blockCommentEnd(src []byte, i int) bool {
	return src[i] == '*' && i+1 < len(src) && src[i+1] == '/'
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '.' || c == '-' || c == '+' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// lex tokenizes proto source. It never fails: anything it cannot classify
// becomes a single-byte punctuation token, so a malformed file still yields
// a reviewable structure.
func lex(src []byte) []token {
	var (
		toks []token
		lead []string
		i    int
		line = 1
		last = -1 // index of the most recently emitted token
	)
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			start, startLine := i, line
			for i < len(src) && src[i] != '\n' {
				i++
			}
			addComment(toks, last, &lead, string(src[start:i]), startLine)
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			start, startLine := i, line
			i += 2
			for i < len(src) && !blockCommentEnd(src, i) {
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i < len(src) {
				i += 2
			}
			addComment(toks, last, &lead, string(src[start:i]), startLine)
		case c == '"' || c == '\'':
			start := i
			quote := c
			i++
			for i < len(src) && src[i] != quote {
				if src[i] == '\\' && i+1 < len(src) {
					i++
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i < len(src) {
				i++
			}
			toks = append(toks, token{kind: tokString, text: string(src[start:i]), start: start, end: i, line: line, lead: takeLead(&lead)})
			last = len(toks) - 1
		case isIdentByte(c):
			start := i
			for i < len(src) && isIdentByte(src[i]) {
				i++
			}
			toks = append(toks, token{kind: tokIdent, text: string(src[start:i]), start: start, end: i, line: line, lead: takeLead(&lead)})
			last = len(toks) - 1
		default:
			toks = append(toks, token{kind: tokPunct, text: string(src[i : i+1]), start: i, end: i + 1, line: line, lead: takeLead(&lead)})
			last = len(toks) - 1
			i++
		}
	}
	return toks
}

// addComment routes a comment either to the preceding token (when it sits on
// that token's line, i.e. a trailing comment) or to the pending leading set
// for whatever token comes next.
func addComment(toks []token, last int, lead *[]string, text string, startLine int) {
	if last >= 0 && toks[last].line == startLine {
		if toks[last].trail == "" {
			toks[last].trail = text
		} else {
			toks[last].trail += " " + text
		}
		return
	}
	*lead = append(*lead, text)
}

func takeLead(lead *[]string) string {
	if len(*lead) == 0 {
		return ""
	}
	s := strings.Join(*lead, " ")
	*lead = (*lead)[:0]
	return s
}
