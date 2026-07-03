package document

import (
	"bytes"
	"fmt"
	"os"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// Parse reads and parses a markdown file into a Document.
func Parse(path string) (*Document, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(path, src)
}

// ParseBytes parses markdown bytes into a Document.
func ParseBytes(path string, src []byte) (*Document, error) {
	root := md.Parser().Parse(text.NewReader(src))
	doc := &Document{Path: path}
	sec := newSectioner()
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		section, ordinal := sec.next(n)
		plain := normalize(nodeText(n, src))
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, src, n); err != nil {
			return nil, fmt.Errorf("render block %s/%d: %w", section, ordinal, err)
		}
		doc.Blocks = append(doc.Blocks, Block{
			ID:        fmt.Sprintf("%s/%d", section, ordinal),
			Section:   section,
			Ordinal:   ordinal,
			Kind:      kindOf(n),
			Level:     headingLevel(n),
			Quote:     quote(plain, 90),
			Hash:      hashText(plain),
			HTML:      buf.String(),
			PlainText: plain,
		})
	}
	return doc, nil
}
