package sqlstmt

import "strings"

// Words that qualify what CREATE/ALTER/DROP makes without being what it makes.
var modifiers = map[string]bool{
	"OR": true, "REPLACE": true, "UNIQUE": true, "TEMP": true, "TEMPORARY": true,
	"UNLOGGED": true, "GLOBAL": true, "LOCAL": true, "RECURSIVE": true,
}

// Object types named in two words, the first of which says nothing alone.
var twoWord = map[string]bool{"MATERIALIZED": true, "FOREIGN": true, "EVENT": true}

// Words between an object type and its name.
var guards = map[string]bool{
	"IF": true, "NOT": true, "EXISTS": true, "CONCURRENTLY": true, "ONLY": true,
	"INTO": true, "FROM": true, "TABLE": true, "IGNORE": true, "ON": true,
}

// classify reads what a statement is from its first words: `ALTER TABLE
// orders …` is ("alter-table", "orders"), `INSERT INTO orders …` is
// ("insert", "orders"), `SELECT …` is ("select", ""). Anything it does not
// recognise is its first word, and an empty statement is "statement".
func classify(text string) (kind, object string) {
	toks := tokens(text, 10)
	if len(toks) == 0 {
		return "statement", ""
	}
	verb := strings.ToUpper(toks[0])
	rest := toks[1:]
	switch verb {
	case "CREATE", "ALTER", "DROP", "COMMENT":
		if verb == "COMMENT" && len(rest) > 0 && strings.EqualFold(rest[0], "ON") {
			rest = rest[1:]
		}
		for len(rest) > 0 && modifiers[strings.ToUpper(rest[0])] {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return strings.ToLower(verb), ""
		}
		typ := strings.ToLower(rest[0])
		rest = rest[1:]
		if twoWord[strings.ToUpper(typ)] && len(rest) > 0 {
			typ += "-" + strings.ToLower(rest[0])
			rest = rest[1:]
		}
		kind = strings.ToLower(verb) + "-" + typ
		if verb == "COMMENT" {
			kind = "comment-on-" + typ
		}
		// An unnamed index is named by the table it is on, which is why ON
		// is a guard word.
		return clean(kind), name(rest)
	case "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REPLACE", "MERGE":
		return strings.ToLower(verb), name(rest)
	}
	return clean(strings.ToLower(verb)), ""
}

// name returns the first token that is not a guard word, unquoted.
func name(toks []string) string {
	for _, t := range toks {
		if guards[strings.ToUpper(t)] {
			continue
		}
		return clean(strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "").Replace(t))
	}
	return ""
}

// clean keeps a kind or a name safe inside a block id: no separators the id
// already uses, nothing that is not printable.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == ':' || r == '#' || r <= ' ':
			return -1
		}
		return r
	}, s)
}

// tokens returns up to n leading tokens of a statement: words and quoted
// identifiers, with comments skipped and punctuation as a boundary.
func tokens(text string, n int) []string {
	var out []string
	b := []byte(text)
	for i := 0; i < len(b) && len(out) < n; {
		c := b[i]
		switch {
		case isSpace(c) || c == '(' || c == ')' || c == ',' || c == ';':
			i++
		case c == '-' && at(b, i+1) == '-', c == '/' && at(b, i+1) == '*':
			i = (&scanner{src: b}).skipComment(i)
		case c == '\'' || c == '$':
			// A literal ends the leading words worth reading.
			return out
		default:
			j := i
			for j < len(b) && !isSpace(b[j]) && b[j] != '(' && b[j] != ')' && b[j] != ',' && b[j] != ';' {
				if b[j] == '"' || b[j] == '`' {
					j = (&scanner{src: b}).skipQuoted(j, b[j])
					continue
				}
				j++
			}
			out = append(out, string(b[i:j]))
			i = j
		}
	}
	return out
}
