package document

import (
	"strings"

	"github.com/gruesomeparty/marginalia/internal/highlight"
	"github.com/yuin/goldmark/ast"
)

// protoLang highlights .proto declarations in the tree renderer, which are
// read as code even though they are rendered as a tree.
var protoLang, _ = highlight.For("proto")

// highlightFence renders a fenced code block with its comments, strings,
// numbers and keywords spanned, and reports false for a language Marginalia
// does not tokenize — in which case the caller renders the fence exactly as
// it did before. Highlighting is done here, at render time, because the page
// must survive a strict CSP: a client-side highlighter would mean a script
// from a CDN, which is the one thing the page may not have.
func highlightFence(fence *ast.FencedCodeBlock, src []byte) (string, bool) {
	info := string(fence.Language(src))
	lang, ok := highlight.For(info)
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString(`<pre><code class="language-`)
	b.WriteString(esc(strings.ToLower(strings.TrimSpace(info))))
	b.WriteString(`">`)
	b.WriteString(lang.HTML(fenceSource(fence, src)))
	b.WriteString("</code></pre>")
	return b.String(), true
}
