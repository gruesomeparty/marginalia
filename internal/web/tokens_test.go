package web

import (
	"regexp"
	"strings"
	"testing"
)

// The design language is enforced here rather than by discipline, because the
// way it decayed the first time was one reasonable-looking rule at a time. A
// new rule that hardcodes a colour or invents a nineteenth font size fails
// this test with the line that did it.

// styleBlock is the page's one stylesheet, and `rules` is it without the
// token and palette declarations — the part that must speak only in tokens.
func styleBlock(t *testing.T) (all, rules string) {
	t.Helper()
	raw, err := files.ReadFile("review.html.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	open, close := strings.Index(s, "<style>"), strings.Index(s, "</style>")
	if open < 0 || close < 0 {
		t.Fatal("the template has no stylesheet")
	}
	all = s[open:close]
	// Everything from the first real rule on. The declarations above it are
	// where literal values are allowed to live.
	start := strings.Index(all, "*{box-sizing:border-box}")
	if start < 0 {
		t.Fatal("cannot find where the declarations end and the rules begin")
	}
	return all, all[start:]
}

func TestRulesUseTokensNotLiterals(t *testing.T) {
	_, rules := styleBlock(t)

	if hexes := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(rules, -1); len(hexes) > 0 {
		t.Errorf("%d raw colour(s) outside the palette declarations: %v — add a token instead", len(hexes), hexes)
	}
	// The sans stack was written out twelve times before this existed.
	if n := strings.Count(rules, "-apple-system"); n != 0 {
		t.Errorf("the sans stack is spelled out %d time(s) in the rules — use var(--sans)", n)
	}
	if n := strings.Count(rules, "ui-monospace"); n != 0 {
		t.Errorf("the mono stack is spelled out %d time(s) in the rules — use var(--mono)", n)
	}

	// A font size is a token, or an em where it must scale with the code
	// around it.
	for _, v := range regexp.MustCompile(`font-size:\s*([^;}]+)`).FindAllStringSubmatch(rules, -1) {
		value := strings.TrimSpace(v[1])
		if strings.HasPrefix(value, "var(--fs-") {
			continue
		}
		if strings.HasSuffix(value, "em") && !strings.HasSuffix(value, "rem") {
			continue // relative to the code it sits in, which is the point
		}
		t.Errorf("font-size:%s is neither a token nor an em — the scale is --fs-2xs…--fs-lg", value)
	}

	for _, v := range regexp.MustCompile(`border-radius:\s*([^;}]+)`).FindAllStringSubmatch(rules, -1) {
		value := strings.TrimSpace(v[1])
		if strings.Contains(value, "var(--r-") || value == "0" {
			continue
		}
		t.Errorf("border-radius:%s is not a token — the shapes are --r-sm…--r-pill", value)
	}

	for _, v := range regexp.MustCompile(`box-shadow:\s*([^;}]+)`).FindAllStringSubmatch(rules, -1) {
		value := strings.TrimSpace(v[1])
		if strings.Contains(value, "var(--e-") || strings.Contains(value, "var(--ring)") || strings.Contains(value, "var(--shadow)") {
			continue
		}
		// A focus ring drawn as two rings is still elevation-free; allow it
		// only when it is built from tokens.
		if !strings.Contains(value, "#") && strings.Contains(value, "var(--") {
			continue
		}
		t.Errorf("box-shadow:%s is not a token — use --e-1, --e-2 or --ring", value)
	}
}

// Every variable a rule reads must be defined, or a theme silently falls back
// to nothing. This catches a typo'd token, which is otherwise invisible.
func TestEveryTokenReadIsDefined(t *testing.T) {
	all, rules := styleBlock(t)
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(all, -1) {
		defined[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`var\((--[a-z0-9-]+)`).FindAllStringSubmatch(rules, -1) {
		name := m[1]
		if defined[name] {
			continue
		}
		// --mg-* are the hooks the drawn diagram's own stylesheet reads; the
		// page defines them on .pic, which this regex sees. --badge* are set
		// per block by a rule, with a fallback at the use.
		if strings.HasPrefix(name, "--mg-") || strings.HasPrefix(name, "--badge") {
			continue
		}
		t.Errorf("var(%s) is read but never defined", name)
	}
}

// A palette defines colours; it must define all of them. --marker was defined
// in the light root and never redefined for dark, so every dark badge wore the
// light palette's yellow until this test existed.
func TestEveryPaletteDefinesEveryColour(t *testing.T) {
	all, _ := styleBlock(t)
	// Each declaration block that sets --bg is a palette: it is claiming to
	// paint the page, so it owes the whole set.
	required := []string{"--bg", "--fg", "--muted", "--rule", "--accent", "--marker", "--card", "--on-accent", "--on-marker", "--ok", "--bad", "--tok-kw", "--tok-str", "--tok-num", "--tok-com"}
	blocks := regexp.MustCompile(`\{[^{}]*--bg:[^{}]*\}`).FindAllString(all, -1)
	if len(blocks) < 5 {
		t.Fatalf("expected the default and Catppuccin palettes in both modes, found %d", len(blocks))
	}
	for i, block := range blocks {
		for _, name := range required {
			if !strings.Contains(block, name+":") {
				t.Errorf("palette %d does not define %s — it will inherit the previous palette's value:\n%.120s…", i, name, block)
			}
		}
	}
}

// The tokens themselves live in one place, so a palette cannot quietly
// redefine a radius or a font size and make two themes lay out differently.
func TestPalettesDefineColoursOnly(t *testing.T) {
	all, _ := styleBlock(t)
	reads := regexp.MustCompile(`var\([^)]*\)`)
	for _, block := range regexp.MustCompile(`\{[^{}]*--bg:[^{}]*\}`).FindAllString(all, -1) {
		// A palette may *read* a shape token — --shadow is palette-defined
		// because its alpha differs per theme, and it is built from --e-1.
		// What it may not do is define one.
		block = reads.ReplaceAllString(block, "")
		for _, prefix := range []string{"--fs-", "--sp-", "--r-", "--e-", "--lh-", "--tint"} {
			if strings.Contains(block, prefix) {
				t.Errorf("a palette defines %s* — palettes are colour only:\n%.120s…", prefix, block)
			}
		}
	}
}
