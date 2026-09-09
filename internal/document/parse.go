package document

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Formats Marginalia can render for review.
const (
	FormatMarkdown = "markdown"
	FormatProto    = "proto"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// FormatFor returns the format Marginalia parses path as, or "" if the
// extension is not supported. It is the single source of truth for which
// inputs `serve` accepts.
func FormatFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return FormatMarkdown
	case ".proto":
		return FormatProto
	}
	return ""
}

// Parse reads and parses a document into blocks, picking the parser from the
// file extension.
func Parse(path string) (*Document, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(path, src)
}

// ParseBytes parses document bytes into a Document. The path decides the
// format and is recorded on the document and on every feedback event.
func ParseBytes(path string, src []byte) (*Document, error) {
	switch FormatFor(path) {
	case FormatMarkdown:
		return parseMarkdown(path, src)
	case FormatProto:
		return parseProto(path, src)
	}
	return nil, fmt.Errorf("no parser for %s", filepath.Ext(path))
}

func parseMarkdown(path string, src []byte) (*Document, error) {
	root := md.Parser().Parse(text.NewReader(src))
	doc := &Document{Path: path, Format: FormatMarkdown}
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
