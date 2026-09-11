package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
)

func TestThemeFor(t *testing.T) {
	cases := map[string]Theme{
		"":                  {Name: "default"},
		"default":           {Name: "default"},
		"dark":              {Name: "dark", Mode: "dark"},
		"light":             {Name: "light", Mode: "light"},
		"catppuccin":        {Name: "catppuccin", Palette: "catppuccin"},
		"Catppuccin-Mocha":  {Name: "catppuccin-mocha", Palette: "catppuccin", Mode: "dark"},
		" catppuccin-latte": {Name: "catppuccin-latte", Palette: "catppuccin", Mode: "light"},
	}
	for name, want := range cases {
		got, err := ThemeFor(name)
		if err != nil {
			t.Errorf("ThemeFor(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("ThemeFor(%q) = %+v, want %+v", name, got, want)
		}
	}
}

// An unknown theme names what there is — the CLI turns that into the
// request-feature pointer, so "I wanted catppuccin-frappe" becomes an issue
// rather than a shrug.
func TestThemeForUnknown(t *testing.T) {
	_, err := ThemeFor("catppuccin-frappe")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"catppuccin-frappe", "catppuccin-mocha", "default"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestThemeNamesSorted(t *testing.T) {
	names := ThemeNames()
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("ThemeNames() unsorted: %v", names)
		}
	}
}

// The palette reaches the page as attributes on <html>, which is what the
// stylesheet switches on — and what the reviewer's own choice overrides.
func TestRenderCarriesTheme(t *testing.T) {
	doc, err := document.ParseBytes("d.md", []byte("# T\n\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	theme, err := ThemeFor("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, Page{Doc: doc, Theme: theme}); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	if !strings.Contains(page, `<html lang="en" data-palette="catppuccin" data-mode="dark">`) {
		t.Errorf("page does not carry the theme: %s", page[:200])
	}
	// The default theme pins nothing, so the OS setting decides.
	buf.Reset()
	if err := Render(&buf, Page{Doc: doc}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `<html lang="en">`) {
		t.Error("the default theme should not pin a palette or a mode")
	}
}

// The page must be able to survive a strict CSP: nothing may be fetched.
func TestPageMakesNoExternalRequests(t *testing.T) {
	doc, err := document.ParseBytes("d.md", []byte("# T\n\n```go\nconst n = 1\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, Page{Doc: doc}); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	for _, forbidden := range []string{"http://", "https://", "//cdn", "@import", "url(", "<link", "src="} {
		if strings.Contains(page, forbidden) {
			t.Errorf("page contains %q, which would mean an external request", forbidden)
		}
	}
}

// Static mode's own no-request test: the offline page is the one that will be
// opened somewhere with a strict CSP and no network at all.
func TestStaticPageLoadsNothing(t *testing.T) {
	doc, err := document.ParseBytes("d.md", []byte("# T\n\n![arch](https://example.com/a.png)\n\n![data](data:image/gif;base64,R0lGOD)\n"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, Page{Doc: doc, Static: true}); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	if strings.Contains(page, `src="https://example.com/a.png"`) {
		t.Error("an external image would be fetched on load")
	}
	if !strings.Contains(page, `data:image/gif;base64,R0lGOD`) {
		t.Error("a data: URI is already self-contained and should be left alone")
	}
	if !strings.Contains(page, `"static":true`) {
		t.Error("the page does not know it has no server")
	}
	// And the parsed document itself is untouched: the source is read-only,
	// and so is what was made from it.
	if !strings.Contains(doc.Blocks[1].HTML, `src="https://example.com/a.png"`) {
		t.Error("rendering the offline page mutated the document")
	}
}

func TestOfflineRewrites(t *testing.T) {
	cases := map[string]string{
		`<p><img src="https://x/y.png" alt="a plan"></p>`: `a plan`,
		`<p><img src="local.png"></p>`:                    `image`,
		`<p>no images here</p>`:                           `no images here`,
	}
	for in, want := range cases {
		if got := offline(in); !strings.Contains(got, want) {
			t.Errorf("offline(%q) = %q, want it to mention %q", in, got, want)
		}
	}
	if got := offline(`<img src="data:image/png;base64,AAA">`); !strings.Contains(got, "<img") {
		t.Errorf("a data: image should survive: %q", got)
	}
}
