// Package sqlstmt splits SQL source into statements, so a note like "this
// ALTER locks the table" lands on that statement rather than on the migration
// that contains it.
//
// Like internal/mermaid it is a scanner, not a grammar: it knows where a
// statement ends — at a semicolon outside strings, quoted identifiers,
// comments, dollar-quoted bodies and BEGIN … END blocks — and what the first
// few words say it is. It never fails. Text it cannot classify is still a
// statement, kept as written, because dropping a line of someone's migration
// would be worse than mislabelling it.
package sqlstmt

import (
	"bytes"
	"strings"
)

// KindComment is a run of comments with no statement after it, kept so the
// end of a file is still reviewable.
const KindComment = "comment"

// Stmt is one statement. Start and End are byte offsets into the source,
// covering the comments directly above the statement and the statement up to
// and including its semicolon, so a renderer can annotate the source in place.
type Stmt struct {
	Kind    string // "alter-table", "create-index", "insert", "select", …
	Object  string // what it acts on, as written without quotes; may be empty
	Text    string // the statement, trimmed, without its leading comments
	Comment string // the comments directly above it, markers stripped
	// Trailing is a comment on the line the statement ends on, markers
	// stripped.
	Trailing string
	Line     int // 1-based line the statement itself starts on
	Start    int
	End      int
}

// Split returns the statements of src in source order.
func Split(src []byte) []Stmt {
	s := &scanner{src: src, line: 1}
	return s.run()
}

type scanner struct {
	src []byte
	out []Stmt
	// The statement being read: where its comments began, where its own
	// text began (-1 between statements), and the comments seen so far.
	seg, start int
	comments   []string
	// depth counts open BEGIN/CASE blocks, inside which a semicolon does
	// not end the statement.
	depth int
	first bool // the next word is the statement's first
	// line is the 1-based line at offset lineAt, so line numbers are counted
	// once over the file rather than from the top for every statement.
	line, lineAt int
}

func (s *scanner) run() []Stmt {
	s.reset()
	src := s.src
	lastComment := 0
	for i := 0; i < len(src); {
		c := src[i]
		if s.start < 0 {
			switch {
			case isSpace(c) || c == ';':
				i++
			case c == '-' && at(src, i+1) == '-', c == '/' && at(src, i+1) == '*':
				end := s.skipComment(i)
				// A comment on the line a statement ends on is about that
				// statement, not the next: it joins its range and stays out
				// of the next one's quote and hash.
				if n := len(s.out); s.seg < 0 && n > 0 && !bytes.ContainsRune(src[s.out[n-1].End:i], '\n') {
					s.out[n-1].End = end
					s.out[n-1].Trailing = strings.TrimSpace(s.out[n-1].Trailing + " " + stripComment(string(src[i:end])))
					i = end
					continue
				}
				if s.seg < 0 {
					s.seg = i
				}
				s.comments = append(s.comments, stripComment(string(src[i:end])))
				lastComment = end
				i = end
			default:
				s.start = i
				if s.seg < 0 {
					s.seg = i
				}
			}
			continue
		}
		switch {
		case c == '-' && at(src, i+1) == '-', c == '/' && at(src, i+1) == '*':
			i = s.skipComment(i)
		case c == '\'':
			i = s.skipString(i, i > 0 && (src[i-1] == 'E' || src[i-1] == 'e') && (i < 2 || !isWord(src[i-2])))
		case c == '"' || c == '`':
			i = s.skipQuoted(i, c)
		case c == '$' && (i == 0 || !isWord(src[i-1])):
			i = s.skipDollar(i)
		case isWord(c):
			i = s.word(i)
		case c == ';' && s.depth == 0:
			s.emit(i + 1)
			i++
		default:
			i++
		}
	}
	switch {
	case s.start >= 0:
		end := len(strings.TrimRight(string(src), " \t\r\n"))
		s.emit(end)
	case s.seg >= 0:
		text := strings.TrimSpace(string(src[s.seg:lastComment]))
		s.out = append(s.out, Stmt{
			Kind:  KindComment,
			Text:  text,
			Line:  s.lineOf(s.seg),
			Start: s.seg,
			End:   lastComment,
		})
	}
	return s.out
}

func (s *scanner) reset() {
	s.seg, s.start, s.comments, s.depth, s.first = -1, -1, nil, 0, true
}

// emit closes the current statement at end.
func (s *scanner) emit(end int) {
	text := strings.TrimSpace(string(s.src[s.start:end]))
	kind, object := classify(text)
	s.out = append(s.out, Stmt{
		Kind:    kind,
		Object:  object,
		Text:    text,
		Comment: strings.Join(s.comments, " "),
		Line:    s.lineOf(s.start),
		Start:   s.seg,
		End:     end,
	})
	s.reset()
}

// word reads one keyword or identifier and tracks the blocks it opens and
// closes: a trigger or procedure body holds semicolons of its own.
func (s *scanner) word(i int) int {
	j := i
	for j < len(s.src) && isWord(s.src[j]) {
		j++
	}
	w := strings.ToUpper(string(s.src[i:j]))
	first := s.first
	s.first = false
	switch w {
	case "BEGIN":
		// `BEGIN;` and `BEGIN TRANSACTION` open a transaction, not a block.
		if first {
			switch next := s.peek(j); next {
			case "", ";", "TRANSACTION", "TRAN", "WORK", "ISOLATION", "READ", "DEFERRED", "IMMEDIATE", "EXCLUSIVE", "DISTRIBUTED":
				return j
			}
		}
		s.depth++
	case "CASE":
		s.depth++
	case "END":
		switch s.peek(j) {
		case "IF", "LOOP", "WHILE", "REPEAT", "FOR":
			// Closes a construct that was never counted.
		case "CASE":
			// `END CASE` closes one CASE: the word after END must not open
			// another.
			if s.depth > 0 {
				s.depth--
			}
			return s.skipWord(j)
		default:
			if s.depth > 0 {
				s.depth--
			}
		}
	}
	return j
}

// skipWord returns the offset past the word that follows i.
func (s *scanner) skipWord(i int) int {
	for i < len(s.src) && isSpace(s.src[i]) {
		i++
	}
	for i < len(s.src) && isWord(s.src[i]) {
		i++
	}
	return i
}

// peek returns the next word after i, upper-cased, or ";" for a semicolon,
// or "" at the end of the source.
func (s *scanner) peek(i int) string {
	for i < len(s.src) && isSpace(s.src[i]) {
		i++
	}
	if i >= len(s.src) {
		return ""
	}
	if s.src[i] == ';' {
		return ";"
	}
	j := i
	for j < len(s.src) && isWord(s.src[j]) {
		j++
	}
	return strings.ToUpper(string(s.src[i:j]))
}

// skipComment returns the offset just past the comment at i. Block comments
// nest, as PostgreSQL reads them; an unterminated one runs to the end.
func (s *scanner) skipComment(i int) int {
	src := s.src
	if src[i] == '-' {
		for i < len(src) && src[i] != '\n' {
			i++
		}
		return i
	}
	depth := 0
	for i < len(src) {
		switch {
		case src[i] == '/' && at(src, i+1) == '*':
			depth++
			i += 2
		case src[i] == '*' && at(src, i+1) == '/':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return i
}

// skipString skips a single-quoted string, where ” is a quote. A backslash
// escapes only in an E” string: in standard SQL it is an ordinary character,
// and honouring it everywhere would swallow the rest of a file after 'C:\'.
func (s *scanner) skipString(i int, backslash bool) int {
	src := s.src
	for i++; i < len(src); i++ {
		switch {
		case backslash && src[i] == '\\':
			i++
		case src[i] == '\'' && at(src, i+1) == '\'':
			i++
		case src[i] == '\'':
			return i + 1
		}
	}
	return i
}

// skipQuoted skips a quoted identifier, where a doubled quote is a quote.
func (s *scanner) skipQuoted(i int, q byte) int {
	src := s.src
	for i++; i < len(src); i++ {
		if src[i] == q {
			if at(src, i+1) == q {
				i++
				continue
			}
			return i + 1
		}
	}
	return i
}

// skipDollar skips a PostgreSQL dollar-quoted body ($$ … $$, $fn$ … $fn$).
// A `$` that does not open one — a `$1` parameter — is a single character.
func (s *scanner) skipDollar(i int) int {
	src := s.src
	j := i + 1
	// A tag is an identifier: it cannot start with a digit, which is how `$1` is told apart.
	for j < len(src) && isWord(src[j]) && (j > i+1 || !isDigit(src[j])) {
		j++
	}
	if j >= len(src) || src[j] != '$' {
		return i + 1
	}
	tag := src[i : j+1]
	end := bytes.Index(src[j+1:], tag)
	if end < 0 {
		return len(src)
	}
	return j + 1 + end + len(tag)
}

// stripComment removes comment markers, so an anchor quotes what the comment
// says rather than its punctuation.
func stripComment(c string) string {
	if strings.HasPrefix(c, "--") {
		return strings.TrimSpace(strings.TrimPrefix(c, "--"))
	}
	c = strings.TrimSuffix(strings.TrimPrefix(c, "/*"), "*/")
	lines := strings.Split(c, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "*"))
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

func at(src []byte, i int) byte {
	if i < len(src) {
		return src[i]
	}
	return 0
}

// lineOf returns the line of offset i, which never precedes the last asked.
func (s *scanner) lineOf(i int) int {
	s.line += bytes.Count(s.src[s.lineAt:i], []byte("\n"))
	s.lineAt = i
	return s.line
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWord(c byte) bool {
	return c == '_' || isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z') || c >= 0x80
}
