package codetree

import (
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	src, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func parse(t *testing.T, path string, src []byte) *File {
	t.Helper()
	l := Detect(path)
	if l == nil {
		t.Fatalf("no language for %s", path)
	}
	f, err := Parse(l, src)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// flat lists every symbol as "path kind", depth-first, indented by depth.
func flat(syms []*Symbol, depth int) []string {
	var out []string
	for _, s := range syms {
		out = append(out, strings.Repeat("  ", depth)+s.Path+" "+s.Kind)
		out = append(out, flat(s.Children, depth+1)...)
	}
	return out
}

// Issue #31's table: each Go declaration anchored by symbol path.
func TestGoAnchorsBySymbolPath(t *testing.T) {
	f := parse(t, "ingest.go", load(t, "ingest.go"))
	want := []string{
		"package package",
		"import import",
		"const const",
		"  const/batchCap const",
		"  const/other const",
		"const/single const",
		"var/x,y var",
		"type type",
		"  A struct",
		"    A/f field",
		"  B type",
		"Batch struct",
		"  Batch/Cap field",
		"  Batch/a,b field",
		"  Batch/fmt.Stringer embed",
		"Doer interface",
		"  Doer/Do method",
		"Batch/Full method",
		"Ingest func",
		"broken func",
	}
	if got := flat(f.Symbols, 0); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("symbols:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A comment directly above a declaration is part of it, one on the same
// line trails it, and one with a blank line after it stands alone.
func TestGoCommentsJoinWhatTheyDocument(t *testing.T) {
	src := []byte("package p\n\n// stray\n\n// Doc.\nfunc F() {} // trail\n")
	f := parse(t, "p.go", src)
	got := map[string]string{}
	for _, s := range f.Symbols {
		got[s.Path] = string(src[s.Start:s.End])
	}
	if got["comment"] != "// stray" {
		t.Errorf("standalone comment = %q", got["comment"])
	}
	if got["F"] != "// Doc.\nfunc F() {} // trail" {
		t.Errorf("F = %q", got["F"])
	}
}

// A file that does not compile still has every non-blank byte inside some
// top-level symbol, so nothing in it is hidden from the reviewer.
func TestBrokenFileHidesNothing(t *testing.T) {
	for _, src := range []string{
		string(load(t, "ingest.go")),
		"package p\n\nfunc (\n\n}}} ))) type\n\nvar x = \"unterminated\n",
		"}}}}",
	} {
		f := parse(t, "p.go", []byte(src))
		covered := make([]bool, len(src))
		for _, s := range f.Symbols {
			for i := s.Start; i < s.End; i++ {
				covered[i] = true
			}
		}
		for i, c := range []byte(src) {
			if !covered[i] && !isSpace(c) {
				t.Errorf("%q: byte %d (%q) is in no symbol: %v", src, i, c, flat(f.Symbols, 0))
				break
			}
		}
	}
}

var tag = regexp.MustCompile(`<[^>]+>`)

// The rendered HTML is the source: strip the spans and unescape, and the
// text is what the file says, less the indentation of the opening line.
func TestHTMLIsTheSource(t *testing.T) {
	src := load(t, "ingest.go")
	f := parse(t, "ingest.go", src)
	var batch *Symbol
	for _, s := range f.Symbols {
		if s.Path == "Batch" {
			batch = s
		}
	}
	for _, s := range append(f.Symbols, batch.Children...) {
		out := f.HTML(src, s.Start, s.End)
		got := html.UnescapeString(tag.ReplaceAllString(out, ""))
		want := string(src[s.Start:s.End])
		if s.Path == "Batch/Cap" {
			want = strings.ReplaceAll(want, "\n\t", "\n")
		}
		if got != want {
			t.Errorf("%s: html reads %q, want %q", s.Path, got, want)
		}
	}
	full := f.HTML(src, 0, len(src))
	for _, want := range []string{`<span class="tok kw">func</span>`, `<span class="tok com">// Full reports.</span>`, `<span class="tok str">&#34;fmt&#34;</span>`, `<span class="tok num">10</span>`} {
		if !strings.Contains(full, want) {
			t.Errorf("highlighting lacks %s", want)
		}
	}
	if strings.Contains(full, "<script") || strings.Contains(f.HTML([]byte(`x := "<b>"`), 0, 10), "<b>") {
		t.Error("source is not escaped")
	}
}

// What releases embed: every language here needs its grammar's subset tag
// in .goreleaser.yaml, or the released binary — unlike `go build` — would
// not have the grammar and every file in that language would show whole.
func TestReleaseEmbedsEveryGrammar(t *testing.T) {
	raw, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`-tags=(\S+)`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal(".goreleaser.yaml builds without -tags; releases would embed all ~200 grammars")
	}
	tags := map[string]bool{}
	for _, t := range strings.Split(m[1], ",") {
		tags[t] = true
	}
	if !tags["grammar_subset"] {
		t.Error("-tags lacks grammar_subset")
	}
	for _, l := range Languages() {
		if !tags["grammar_subset_"+l.Grammar] {
			t.Errorf("-tags lacks grammar_subset_%s, which %s needs", l.Grammar, l.Name)
		}
	}
}

// The tags are only strings until the library reads them: build the
// grammars package the way a release does and check each grammar is really
// embedded, so a tag gotreesitter does not recognise fails here rather than
// shipping a binary that shows every file whole.
func TestReleaseTagsEmbedTheGrammars(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	raw, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`-tags=(\S+)`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("no -tags in .goreleaser.yaml")
	}
	out, err := exec.Command(gobin, "list", "-tags="+m[1], "-f", "{{join .EmbedFiles \" \"}}",
		"github.com/odvcencio/gotreesitter/grammars").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	embedded := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		embedded[filepath.Base(f)] = true
	}
	for _, l := range Languages() {
		if !embedded[l.Grammar+".bin"] {
			t.Errorf("a release build does not embed %s.bin; %s files would show whole (embedded: %s)", l.Grammar, l.Name, out)
		}
	}
	if len(embedded) > len(Languages()) {
		t.Errorf("a release build embeds %d grammars for %d languages: %s", len(embedded), len(Languages()), out)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(string(load(&testing.T{}, "ingest.go")))
	f.Add("package p\nfunc (")
	f.Add("type ( A struct { B interface { C } } )")
	f.Fuzz(func(t *testing.T, src string) {
		file, err := Parse(Detect("x.go"), []byte(src))
		if err != nil {
			return // refused whole; the caller shows the file
		}
		var walk func([]*Symbol, int, int)
		walk = func(syms []*Symbol, lo, hi int) {
			at := lo
			for _, s := range syms {
				if s.Start < at || s.End < s.Start || s.End > hi {
					t.Fatalf("%s at [%d,%d) outside [%d,%d) after %d", s.Path, s.Start, s.End, lo, hi, at)
				}
				walk(s.Children, s.Start, s.End)
				at = s.End
			}
		}
		walk(file.Symbols, 0, len(src))
		_ = file.HTML([]byte(src), 0, len(src))
	})
}

// Dedenting a method read out of a type leaves a raw string's own
// indentation alone.
func TestHTMLKeepsIndentationInsideStrings(t *testing.T) {
	src := []byte("type T struct {\n\tA string `tag`\n}\n\nvar _ = func() {\n\tq := `\n\t\tSELECT 1\n\t`\n}\n")
	f := parse(t, "p.go", src)
	start := strings.Index(string(src), "q :=")
	out := html.UnescapeString(tag.ReplaceAllString(f.HTML(src, start, len(src)-3), ""))
	if !strings.Contains(out, "\n\t\tSELECT 1\n\t`") {
		t.Errorf("raw string re-indented: %q", out)
	}
}
