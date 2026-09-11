// Package highlight tokenizes source code into spans for the review page.
//
// It runs at render time, on the server, on purpose: a client-side
// highlighter means a script from a CDN, and the review page must survive a
// strict CSP with no external requests at all. So this is deliberately small
// and boring — comments, strings, numbers and keywords, in a handful of
// languages, with graceful passthrough for everything else. Reading code well
// enough to review it does not need a full parser; it needs the comments to
// recede and the strings to stand out.
package highlight

import (
	"html"
	"sort"
	"strings"
)

// Token classes, which are also the CSS class names the page styles.
const (
	classKeyword = "kw"
	classString  = "str"
	classNumber  = "num"
	classComment = "com"
)

// Lang is one language's lexical shape: what a comment looks like, what
// quotes a string, and which words are keywords.
type Lang struct {
	Name        string
	lineComment []string
	blockOpen   string
	blockClose  string
	quotes      string
	keywords    map[string]bool
}

// For returns the language for a fence's info string ("go", "yaml", "sh"),
// and false when there is none — in which case the caller renders the code
// as it always did.
func For(tag string) (Lang, bool) {
	l, ok := langs[strings.ToLower(strings.TrimSpace(tag))]
	return l, ok
}

// Names lists the languages that are highlighted, for documentation and
// error messages.
func Names() []string {
	out := make([]string, 0, len(langs))
	for name := range langs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func words(list string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(list) {
		set[w] = true
	}
	return set
}

// HTML renders src as escaped HTML with a span around every token that is
// not plain code. The text is preserved byte for byte: strip the spans and
// the source reads exactly as it was written, which is the only way an
// anchored quote and a rendered block can be the same thing.
func (l Lang) HTML(src string) string {
	var b strings.Builder
	b.Grow(len(src) + len(src)/4)
	plain := 0 // start of the run of unclassified text not yet written
	flush := func(to int) {
		if to > plain {
			b.WriteString(html.EscapeString(src[plain:to]))
		}
	}
	emit := func(from, to int, class string) {
		flush(from)
		b.WriteString(`<span class="tok `)
		b.WriteString(class)
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(src[from:to]))
		b.WriteString(`</span>`)
		plain = to
	}
	for i := 0; i < len(src); {
		if end, ok := l.comment(src, i); ok {
			emit(i, end, classComment)
			i = end
			continue
		}
		if end, ok := l.str(src, i); ok {
			emit(i, end, classString)
			i = end
			continue
		}
		if isDigit(src[i]) && !identByte(prevByte(src, i)) {
			end := i
			for end < len(src) && (isDigit(src[end]) || src[end] == '.' || src[end] == 'x' ||
				src[end] == '_' || isHex(src[end])) {
				end++
			}
			emit(i, end, classNumber)
			i = end
			continue
		}
		if identStart(src[i]) {
			end := i
			for end < len(src) && identByte(src[end]) {
				end++
			}
			if l.keywords[src[i:end]] {
				emit(i, end, classKeyword)
			}
			i = end
			continue
		}
		i++
	}
	flush(len(src))
	return b.String()
}

// comment reports the end of a comment starting at i.
func (l Lang) comment(src string, i int) (int, bool) {
	for _, prefix := range l.lineComment {
		if !strings.HasPrefix(src[i:], prefix) {
			continue
		}
		end := strings.IndexByte(src[i:], '\n')
		if end < 0 {
			return len(src), true
		}
		return i + end, true
	}
	if l.blockOpen != "" && strings.HasPrefix(src[i:], l.blockOpen) {
		rest := i + len(l.blockOpen)
		if end := strings.Index(src[rest:], l.blockClose); end >= 0 {
			return rest + end + len(l.blockClose), true
		}
		// Unterminated: the rest of the block is a comment, which is what an
		// editor shows too.
		return len(src), true
	}
	return 0, false
}

// str reports the end of a string literal starting at i. A quote that never
// closes claims only its own line, so one stray apostrophe cannot paint the
// rest of the file.
func (l Lang) str(src string, i int) (int, bool) {
	q := src[i]
	if !strings.ContainsRune(l.quotes, rune(q)) {
		return 0, false
	}
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			if q != '`' {
				j++
			}
		case '\n':
			if q == '`' {
				continue
			}
			return j, true
		case q:
			return j + 1, true
		}
	}
	return len(src), true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool   { return c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func identStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
func identByte(c byte) bool { return identStart(c) || isDigit(c) || c == '-' }

func prevByte(src string, i int) byte {
	if i == 0 {
		return ' '
	}
	return src[i-1]
}

// langs is the supported set. Adding one is a line here; anything not listed
// renders as plain text rather than badly.
var langs = map[string]Lang{}

func register(l Lang, names ...string) {
	for _, n := range names {
		langs[n] = l
	}
}

func init() {
	register(Lang{
		Name: "go", lineComment: []string{"//"}, blockOpen: "/*", blockClose: "*/", quotes: "\"'`",
		keywords: words(`break case chan const continue default defer else fallthrough for func go goto
			if import interface map package range return select struct switch type var
			bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string
			uint uint8 uint16 uint32 uint64 uintptr any true false nil iota make new len cap append copy delete panic recover`),
	}, "go", "golang")

	register(Lang{
		Name: "rust", lineComment: []string{"//"}, blockOpen: "/*", blockClose: "*/", quotes: "\"'",
		keywords: words(`as async await break const continue crate dyn else enum extern false fn for if impl in
			let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while
			bool char f32 f64 i8 i16 i32 i64 isize str u8 u16 u32 u64 usize String Vec Option Result Some None Ok Err`),
	}, "rust", "rs")

	register(Lang{
		Name: "js", lineComment: []string{"//"}, blockOpen: "/*", blockClose: "*/", quotes: "\"'`",
		keywords: words(`async await break case catch class const continue debugger default delete do else export extends
			finally for function get if import in instanceof let new of return set static super switch this throw try
			typeof var void while with yield true false null undefined
			interface type enum implements declare readonly public private protected as satisfies keyof namespace`),
	}, "js", "javascript", "jsx", "ts", "typescript", "tsx")

	register(Lang{
		Name: "python", lineComment: []string{"#"}, quotes: "\"'",
		keywords: words(`and as assert async await break class continue def del elif else except finally for from global
			if import in is lambda nonlocal not or pass raise return try while with yield True False None self
			int str float bool bytes list dict set tuple print len range`),
	}, "python", "py")

	register(Lang{
		Name: "shell", lineComment: []string{"#"}, quotes: "\"'",
		keywords: words(`if then else elif fi for while until do done case esac function return in select time
			echo cd export local readonly set unset shift source exit trap`),
	}, "sh", "bash", "shell", "zsh", "console")

	register(Lang{
		Name: "sql", lineComment: []string{"--"}, blockOpen: "/*", blockClose: "*/", quotes: "\"'",
		keywords: words(`SELECT FROM WHERE GROUP BY ORDER HAVING LIMIT OFFSET INSERT INTO VALUES UPDATE SET DELETE
			CREATE TABLE INDEX VIEW ALTER DROP JOIN LEFT RIGHT INNER OUTER FULL ON AS AND OR NOT NULL IS IN LIKE
			BETWEEN DISTINCT COUNT SUM AVG MIN MAX CASE WHEN THEN ELSE END WITH RETURNING PRIMARY KEY FOREIGN REFERENCES
			select from where group by order having limit offset insert into values update set delete
			create table index view alter drop join left right inner outer full on as and or not null is in like
			between distinct count sum avg min max case when then else end with returning primary key foreign references`),
	}, "sql", "postgres", "psql")

	register(Lang{
		Name: "json", quotes: "\"",
		keywords: words(`true false null`),
	}, "json", "jsonc")

	register(Lang{
		Name: "yaml", lineComment: []string{"#"}, quotes: "\"'",
		keywords: words(`true false null yes no on off True False Null ~`),
	}, "yaml", "yml")

	register(Lang{
		Name: "toml", lineComment: []string{"#"}, quotes: "\"'",
		keywords: words(`true false`),
	}, "toml", "ini")

	register(Lang{
		Name: "proto", lineComment: []string{"//"}, blockOpen: "/*", blockClose: "*/", quotes: "\"'",
		keywords: words(`syntax package import option message enum service rpc returns oneof map repeated optional
			required reserved extend extensions stream public weak
			double float int32 int64 uint32 uint64 sint32 sint64 fixed32 fixed64 sfixed32 sfixed64 bool string bytes true false`),
	}, "proto", "proto3", "protobuf")

	register(Lang{
		Name: "http", lineComment: []string{"#"}, quotes: "\"",
		keywords: words(`GET POST PUT PATCH DELETE HEAD OPTIONS HTTP`),
	}, "http")
}
