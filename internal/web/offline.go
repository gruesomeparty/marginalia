package web

import (
	"regexp"
	"strings"
)

// imgTag matches a rendered image. Static mode has to neutralize these: a
// shared review page must make no request at all when it is opened, and an
// `![](https://…)` in the document would otherwise fetch on load — from a
// machine, and to a host, neither the author nor the reviewer chose.
var imgTag = regexp.MustCompile(`(?is)<img\s[^>]*>`)

// srcAttr and altAttr pull the parts worth telling the reader about.
var (
	srcAttr = regexp.MustCompile(`(?is)\bsrc\s*=\s*"([^"]*)"`)
	altAttr = regexp.MustCompile(`(?is)\balt\s*=\s*"([^"]*)"`)
)

// offline rewrites a rendered block for a page that must load nothing. An
// image is replaced by what it was — its alt text and its URL — because the
// reviewer needs to know something is missing there, and an agent reading
// their note needs to know which image they meant. A data: URI is already
// self-contained and is left alone.
func offline(html string) string {
	return imgTag.ReplaceAllStringFunc(html, func(tag string) string {
		src := attr(srcAttr, tag)
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(src)), "data:") {
			return tag
		}
		label := attr(altAttr, tag)
		if label == "" {
			label = "image"
		}
		out := `<span class="offline" title="not loaded: this page makes no requests">` + label
		if src != "" {
			out += ` <code>` + src + `</code>`
		}
		return out + `</span>`
	})
}

// attr returns the first capture of re in tag, already escaped by the
// renderer that wrote it.
func attr(re *regexp.Regexp, tag string) string {
	if m := re.FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}
