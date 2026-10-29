package web

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every palette has to be readable in both modes, the diagram's recolouring
// included. This reads the colours straight out of tokens.css and checks the
// pairs the pages actually put on top of each other against WCAG AA: 4.5:1
// for text, 3:1 for a shape or border that carries meaning. No browser: the
// palettes are plain hex, and the pairs are the contract.

// pair is one foreground drawn on one background, and why it matters.
type pair struct {
	fg, bg string
	min    float64
	use    string
}

const (
	textAA = 4.5
	uiAA   = 3.0
)

// pairs is every combination the stylesheets draw. Adding a rule that puts a
// colour token on a new background means adding it here.
var pairs = []pair{
	{"--fg", "--bg", textAA, "body text"},
	{"--fg", "--card", textAA, "text on a card, and diagram labels on a shape (--mg-text on --mg-node-fill)"},
	{"--muted", "--bg", textAA, "secondary text"},
	{"--muted", "--card", textAA, "secondary text on a card"},
	{"--accent", "--bg", textAA, "links and accent text"},
	{"--accent", "--card", textAA, "accent text on a card"},
	{"--ok", "--bg", textAA, "approve and confirmed text"},
	{"--ok", "--card", textAA, "approve text on a card"},
	{"--bad", "--bg", textAA, "reject, stale and error text"},
	{"--bad", "--card", textAA, "error text on a card"},
	{"--on-accent", "--accent", textAA, "primary buttons"},
	{"--on-marker", "--marker", textAA, "the note-count badge"},
	{"--on-accent", "--ok", textAA, "an approved or confirmed badge"},
	{"--on-accent", "--bad", textAA, "a rejected badge"},
	{"--bg", "--ok", textAA, "the pressed Approve chip"},
	{"--bg", "--bad", textAA, "the pressed Reject chip"},
	{"--bg", "--fg", textAA, "the toast"},
	{"--tok-kw", "--card", textAA, "highlighted keywords in code"},
	{"--tok-str", "--card", textAA, "highlighted strings in code"},
	{"--tok-num", "--card", textAA, "highlighted numbers in code"},
	{"--tok-com", "--card", textAA, "highlighted comments in code"},
	{"--muted", "--card", uiAA, "diagram edges (--mg-line) against a shape"},
	{"--marker-ink", "--bg", uiAA, "a diagram edge or inbox row marked as having notes"},
	{"--marker-ink", "--card", uiAA, "a diagram outline marked as having notes"},
	{"--accent", "--card", uiAA, "focus ring and hovered diagram shape"},
}

// palette is one resolved set of colours: the base palette's tokens with its
// mode's layered on top, the way the cascade applies them.
type palette struct {
	name   string
	colour map[string]string
}

var declRE = regexp.MustCompile(`(--[a-z0-9-]+):([^;}]+)`)

// blockOf returns the declarations of the rule whose selector is exactly sel.
func blockOf(t *testing.T, css, sel string) map[string]string {
	t.Helper()
	i := strings.Index(css, sel+"{")
	if i < 0 {
		t.Fatalf("tokens.css has no rule %s", sel)
	}
	body := css[i+len(sel)+1:]
	body = body[:strings.Index(body, "}")]
	out := map[string]string{}
	for _, m := range declRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

func palettes(t *testing.T) []palette {
	t.Helper()
	css := tokens(t)
	// The first bare :root is the palette-invariant scale; the colour one is
	// the :root that defines --bg.
	var light map[string]string
	for _, m := range regexp.MustCompile(`(?m)^:root\{[^}]*--bg:[^}]*\}`).FindAllString(css, -1) {
		light = blockOf(t, m, ":root")
	}
	if light == nil {
		t.Fatal("tokens.css has no default palette")
	}
	layer := func(base, over map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	dark := layer(light, blockOf(t, css, `:root[data-mode="dark"]`))
	cat := layer(light, blockOf(t, css, `:root[data-palette="catppuccin"]`))
	catDark := layer(cat, blockOf(t, css, `:root[data-palette="catppuccin"][data-mode="dark"]`))
	return []palette{
		{"default light", light},
		{"default dark", dark},
		{"catppuccin light", cat},
		{"catppuccin dark", catDark},
	}
}

func TestEveryPaletteMeetsContrastAA(t *testing.T) {
	for _, p := range palettes(t) {
		for _, c := range pairs {
			fg, err := rgb(p.colour[c.fg])
			if err != nil {
				t.Fatalf("%s %s: %v", p.name, c.fg, err)
			}
			bg, err := rgb(p.colour[c.bg])
			if err != nil {
				t.Fatalf("%s %s: %v", p.name, c.bg, err)
			}
			if got := contrast(fg, bg); got < c.min {
				t.Errorf("%s: %s %s on %s %s is %.2f:1, needs %.1f:1 (%s)",
					p.name, c.fg, p.colour[c.fg], c.bg, p.colour[c.bg], got, c.min, c.use)
			}
		}
	}
}

// The OS-dark media query repeats each dark palette so it applies before the
// reviewer has chosen; if the two copies drift, a reviewer sees different
// colours depending on how they got to dark mode, and only one copy is
// checked above.
func TestMediaQueryDarkMatchesExplicitDark(t *testing.T) {
	css := tokens(t)
	for _, c := range []struct{ explicit, media string }{
		{`:root[data-mode="dark"]`, `:root:not([data-mode="light"])`},
		{`:root[data-palette="catppuccin"][data-mode="dark"]`, `:root[data-palette="catppuccin"]:not([data-mode="light"])`},
	} {
		a, b := blockOf(t, css, c.explicit), blockOf(t, css, c.media)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("%s and %s differ:\n%v\n%v", c.explicit, c.media, a, b)
		}
	}
}

func TestContrastMath(t *testing.T) {
	black, _ := rgb("#000")
	white, _ := rgb("#fff")
	if got := contrast(black, white); math.Abs(got-21) > 0.01 {
		t.Fatalf("black on white = %.2f, want 21", got)
	}
	grey, _ := rgb("#767676")
	if got := contrast(grey, white); got < 4.5 || got > 4.6 {
		t.Fatalf("#767676 on white = %.2f, want ~4.54 (the AA boundary grey)", got)
	}
}

// rgb parses #rgb or #rrggbb into 0–1 channels.
func rgb(hex string) ([3]float64, error) {
	var out [3]float64
	h := strings.TrimPrefix(hex, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 || !strings.HasPrefix(hex, "#") {
		return out, fmt.Errorf("%q is not a hex colour", hex)
	}
	for i := range 3 {
		v, err := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		if err != nil {
			return out, err
		}
		out[i] = float64(v) / 255
	}
	return out, nil
}

// luminance is WCAG 2's relative luminance.
func luminance(c [3]float64) float64 {
	lin := func(v float64) float64 {
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c[0]) + 0.7152*lin(c[1]) + 0.0722*lin(c[2])
}

func contrast(a, b [3]float64) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
