package diagram

import (
	"regexp"
	"strings"
)

// Everything below exists because rendered SVG goes straight into our page.
// It comes from mermaid, not from a stranger — but it is generated markup
// built from document text, and the page it lands in is the one place this
// tool promises makes no requests and runs no code it did not write.

var (
	scriptTag  = regexp.MustCompile(`(?is)<script\b.*?</script\s*>`)
	selfScript = regexp.MustCompile(`(?is)<script\b[^>]*/?>`)
	// on*="…" and on*='…' — inline handlers, in any casing.
	handlerAttr = regexp.MustCompile(`(?is)\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	// href/src/xlink:href values. Anything that is not a fragment or a data:
	// URI would be a fetch on open.
	linkAttr = regexp.MustCompile(`(?is)\s(?:xlink:)?(?:href|src)\s*=\s*"([^"]*)"`)
	// A stylesheet import inside <style> would fetch too.
	cssImport = regexp.MustCompile(`(?is)@import\b[^;]*;?`)
	// mermaid-cli 12 embeds the webfont it measured labels with: four data:
	// fonts, 160 KB per drawing, under a licence comment naming the OFL's
	// URL. The page promises system fonts, and a strict CSP refuses a data:
	// font anyway, so the faces go and the labels fall back to the sans
	// family the same rule names. That rule's own comment goes with them.
	styleBody = regexp.MustCompile(`(?is)(<style\b[^>]*>)(.*?)(</style\s*>)`)
	fontFace  = regexp.MustCompile(`(?is)@font-face\s*\{[^}]*\}`)
	cssNote   = regexp.MustCompile(`(?s)/\*.*?\*/`)
	emptyCSS  = regexp.MustCompile(`(?is)<style\b[^>]*>\s*</style\s*>`)
	svgOpen   = regexp.MustCompile(`(?is)<svg\b`)
)

// sanitize strips from rendered SVG everything that could execute or fetch:
// scripts, inline handlers, external references, CSS imports and fonts. mermaid does
// not emit any of them today; this is what keeps that true tomorrow, and what
// lets the same markup go into a shared file that must survive a strict CSP.
func sanitize(svg string) string {
	if i := svgOpen.FindStringIndex(svg); i != nil {
		svg = svg[i[0]:] // drop any XML prolog or doctype
	}
	svg = scriptTag.ReplaceAllString(svg, "")
	svg = selfScript.ReplaceAllString(svg, "")
	svg = handlerAttr.ReplaceAllString(svg, "")
	svg = cssImport.ReplaceAllString(svg, "")
	svg = styleBody.ReplaceAllStringFunc(svg, func(style string) string {
		m := styleBody.FindStringSubmatch(style)
		css := cssNote.ReplaceAllString(m[2], "")
		return m[1] + fontFace.ReplaceAllString(css, "") + m[3]
	})
	svg = emptyCSS.ReplaceAllString(svg, "")
	svg = linkAttr.ReplaceAllStringFunc(svg, func(attr string) string {
		m := linkAttr.FindStringSubmatch(attr)
		if m == nil {
			return attr
		}
		if local(m[1]) {
			return attr
		}
		return ""
	})
	return strings.TrimSpace(svg)
}

// local reports whether a reference resolves inside the page: a fragment, or
// data already in it.
func local(value string) bool {
	v := strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(v, "#") || strings.HasPrefix(v, "data:")
}
