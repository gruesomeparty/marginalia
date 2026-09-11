package web

import (
	"fmt"
	"sort"
	"strings"
)

// Theme is how the page is painted: a palette, and the light/dark mode the
// person who served it pinned, if any. The reviewer's own choice — remembered
// in their browser, never on disk — wins over both, because the room the
// document is being read in is not something an agent can know.
type Theme struct {
	Name    string `json:"name"`
	Palette string `json:"palette,omitempty"` // "" is the built-in palette
	Mode    string `json:"mode,omitempty"`    // "", "light" or "dark"
}

// themes are the palettes the page ships. They are variable sets in one
// embedded stylesheet — a theme must never cost an external request.
var themes = map[string]Theme{
	"default":          {},
	"light":            {Mode: "light"},
	"dark":             {Mode: "dark"},
	"catppuccin":       {Palette: "catppuccin"},
	"catppuccin-latte": {Palette: "catppuccin", Mode: "light"},
	"catppuccin-mocha": {Palette: "catppuccin", Mode: "dark"},
}

// ThemeFor resolves a theme name. An empty name is the default; an unknown
// one is an error naming what there is, which the CLI turns into the
// request-feature pointer — "I wanted catppuccin-frappe" is a feature
// request, not a typo to swallow.
func ThemeFor(name string) (Theme, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "default"
	}
	t, ok := themes[name]
	if !ok {
		return Theme{}, fmt.Errorf("unknown theme %q — available: %s", name, strings.Join(ThemeNames(), ", "))
	}
	t.Name = name
	return t, nil
}

// ThemeNames lists the available themes.
func ThemeNames() []string {
	out := make([]string, 0, len(themes))
	for name := range themes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
