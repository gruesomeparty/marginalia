package highlight

import (
	"html"
	"strings"
	"testing"
)

// strip removes the spans, which must leave the source exactly as it was: a
// highlighted block and its anchored quote have to be the same text.
func strip(rendered string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(rendered); i++ {
		switch c := rendered[i]; {
		case c == '<':
			depth++
		case c == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteByte(c)
		}
	}
	return html.UnescapeString(b.String())
}

func render(t *testing.T, tag, src string) string {
	t.Helper()
	lang, ok := For(tag)
	if !ok {
		t.Fatalf("no language for %q", tag)
	}
	out := lang.HTML(src)
	if got := strip(out); got != src {
		t.Fatalf("highlighting changed the source:\n%q\nwant:\n%q", got, src)
	}
	return out
}

func TestGo(t *testing.T) {
	out := render(t, "go", "// a note\nfunc main() {\n\ts := \"hi\" // trailing\n\tn := 42\n}\n")
	for _, want := range []string{
		`<span class="tok com">// a note</span>`,
		`<span class="tok kw">func</span>`,
		`<span class="tok str">&#34;hi&#34;</span>`,
		`<span class="tok num">42</span>`,
		`<span class="tok com">// trailing</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
	// main is not a keyword, so it stays plain — the point is that comments
	// recede and strings stand out, not that everything gets a colour.
	if strings.Contains(out, `>main<`) {
		t.Errorf("main should not be classified:\n%s", out)
	}
}

func TestBlockCommentAndBackticks(t *testing.T) {
	out := render(t, "go", "/* two\n   lines */\nvar q = `raw \\n string`\n")
	if !strings.Contains(out, `<span class="tok com">/* two`) {
		t.Errorf("block comment not spanned:\n%s", out)
	}
	if !strings.Contains(out, "<span class=\"tok str\">`raw \\n string`</span>") {
		t.Errorf("raw string not spanned:\n%s", out)
	}
}

// An unterminated quote must not paint the rest of the file.
func TestUnterminatedStringStopsAtTheLine(t *testing.T) {
	out := render(t, "python", "s = 'oops\nn = 42\n")
	if !strings.Contains(out, `<span class="tok num">42</span>`) {
		t.Errorf("the next line should still highlight:\n%s", out)
	}
}

func TestEscapedQuote(t *testing.T) {
	out := render(t, "go", `s := "a \" b"`+"\nn := 7\n")
	if !strings.Contains(out, `<span class="tok num">7</span>`) {
		t.Errorf("escaped quote swallowed the rest:\n%s", out)
	}
}

// A word that only contains a keyword is not a keyword.
func TestKeywordBoundaries(t *testing.T) {
	out := render(t, "go", "iffy := formatted\n")
	if strings.Contains(out, `class="tok kw"`) {
		t.Errorf("substring matched as a keyword:\n%s", out)
	}
}

// Identifiers with digits in them are not numbers.
func TestNumbersNeedABoundary(t *testing.T) {
	out := render(t, "go", "sha256 := 0x1f\n")
	if strings.Contains(out, `<span class="tok num">256`) {
		t.Errorf("digits inside an identifier classified:\n%s", out)
	}
	if !strings.Contains(out, `<span class="tok num">0x1f</span>`) {
		t.Errorf("hex literal not classified:\n%s", out)
	}
}

func TestOtherLanguages(t *testing.T) {
	cases := []struct{ tag, src, want string }{
		{"yaml", "# note\nport: 8080\nquiet: true\n", `<span class="tok com"># note</span>`},
		{"json", `{"a": true, "b": 2}`, `<span class="tok kw">true</span>`},
		{"sh", "# build\nfor f in *; do echo \"$f\"; done\n", `<span class="tok kw">for</span>`},
		{"sql", "-- daily\nSELECT count(*) FROM orders;\n", `<span class="tok com">-- daily</span>`},
		{"proto", "message Order {\n  string id = 1;\n}\n", `<span class="tok kw">message</span>`},
		{"ts", "export const n: number = 1;\n", `<span class="tok kw">export</span>`},
		{"rust", "fn main() { let x = 1; }\n", `<span class="tok kw">fn</span>`},
	}
	for _, tc := range cases {
		out := render(t, tc.tag, tc.src)
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: missing %s in:\n%s", tc.tag, tc.want, out)
		}
	}
}

func TestUnknownLanguage(t *testing.T) {
	for _, tag := range []string{"", "brainfuck", "COBOL", "mermaid"} {
		if _, ok := For(tag); ok {
			t.Errorf("For(%q) should have no language", tag)
		}
	}
}

func TestNamesAreSorted(t *testing.T) {
	names := Names()
	if len(names) < 10 {
		t.Fatalf("suspiciously few languages: %v", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("Names() is not sorted: %v", names)
		}
	}
}

// Markup in the source is escaped, not executed: a code fence is the one
// place a document's own angle brackets are guaranteed to be data.
func TestEscapesMarkup(t *testing.T) {
	out := render(t, "go", "s := \"<script>alert(1)</script>\"\n")
	if strings.Contains(out, "<script>") {
		t.Fatalf("markup came through unescaped:\n%s", out)
	}
}
