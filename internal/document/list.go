package document

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark/ast"
)

// listItemKind is the block kind of a single bullet or numbered item.
const listItemKind = "list_item"

// renderList renders a markdown list and annotates every item as its own
// anchorable block, so a note about one bullet lands on that bullet instead of
// on the whole list. The markup is built here rather than handed to goldmark
// whole because each `<li>` has to carry its own anchor — and because a
// reviewer should never have to promote bullets to headings to make them
// commentable.
//
// The list itself stays a block with the ID it always had: a note about the
// shape of the list ("this should be a table") still has somewhere to live,
// and feedback written before item anchoring existed still anchors.
func (p *mdParser) renderList(list *ast.List, section, parentID string, level int) (string, error) {
	var b strings.Builder
	if list.IsOrdered() {
		b.WriteString("<ol")
		if list.Start > 1 {
			fmt.Fprintf(&b, ` start="%d"`, list.Start)
		}
		b.WriteString(">\n")
	} else {
		b.WriteString("<ul>\n")
	}
	ordinal := 0
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		ordinal++
		id := fmt.Sprintf("%s.%d", parentID, ordinal)
		plain := normalize(itemText(item, p.src))
		// Appended before its own nested items so blocks stay in document
		// order, which is what the page and the fold controls assume.
		p.blocks = append(p.blocks, Block{
			ID:      id,
			Section: section,
			Ordinal: ordinal,
			Kind:    listItemKind,
			Level:   level,
			Parent:  parentID,
			// The markup for an item lives inside its list's HTML, where the
			// document's own structure keeps bullets, numbering and nesting
			// intact; the page renders no separate element for it.
			Inline:    true,
			Quote:     quote(plain, 90),
			Hash:      hashText(plain),
			PlainText: plain,
		})
		at := len(p.blocks) - 1
		content, nested, err := p.renderItem(item, section, id, level)
		if err != nil {
			return "", err
		}
		p.blocks[at].HasChildren = nested != ""
		fmt.Fprintf(&b, `<li class="block" data-block="%s" data-hash="%s" data-quote="%s" data-kind="%s" tabindex="0" role="button" aria-label="Block %s">`,
			esc(id), esc(hashText(plain)), esc(quote(plain, 90)), listItemKind, esc(id))
		b.WriteString(content)
		b.WriteString(nested)
		b.WriteString("</li>\n")
	}
	if list.IsOrdered() {
		b.WriteString("</ol>")
	} else {
		b.WriteString("</ul>")
	}
	return b.String(), nil
}

// renderItem renders one item's own content and, separately, the lists nested
// under it — the item's anchor covers what it says itself, so editing a
// sub-bullet does not stale the bullet above it.
func (p *mdParser) renderItem(item ast.Node, section, id string, level int) (content, nested string, err error) {
	var own, sub strings.Builder
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if list, ok := child.(*ast.List); ok {
			html, err := p.renderList(list, section, id, level+1)
			if err != nil {
				return "", "", err
			}
			sub.WriteString(html)
			continue
		}
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, p.src, child); err != nil {
			return "", "", err
		}
		own.Write(buf.Bytes())
	}
	return own.String(), sub.String(), nil
}

// itemText is a list item's own text, excluding any list nested under it.
func itemText(item ast.Node, source []byte) string {
	var b strings.Builder
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if _, ok := child.(*ast.List); ok {
			continue
		}
		b.WriteString(nodeText(child, source))
		b.WriteByte(' ')
	}
	return b.String()
}
