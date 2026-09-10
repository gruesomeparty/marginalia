package document

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Formats Marginalia can render for review.
const (
	FormatMarkdown = "markdown"
	FormatProto    = "proto"
	FormatJSON     = "json"
	FormatYAML     = "yaml"
	FormatTOML     = "toml"
	FormatMermaid  = "mermaid"
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
	case ".json":
		return FormatJSON
	case ".yaml", ".yml":
		return FormatYAML
	case ".toml":
		return FormatTOML
	case ".mmd", ".mermaid":
		return FormatMermaid
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
	case FormatJSON:
		return parseJSON(path, src)
	case FormatYAML:
		return parseYAML(path, src)
	case FormatTOML:
		return parseTOML(path, src)
	case FormatMermaid:
		return parseMermaid(path, src)
	}
	return nil, fmt.Errorf("no parser for %s", filepath.Ext(path))
}

// mdParser accumulates a markdown document's blocks. Lists add blocks of
// their own while rendering, so the walk carries state.
type mdParser struct {
	src    []byte
	blocks []Block
}

func parseMarkdown(path string, src []byte) (*Document, error) {
	root := md.Parser().Parse(text.NewReader(src))
	p := &mdParser{src: src}
	sec := newSectioner()
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		section, ordinal := sec.next(n)
		id := fmt.Sprintf("%s/%d", section, ordinal)
		plain := normalize(nodeText(n, src))
		block := Block{
			ID:        id,
			Section:   section,
			Ordinal:   ordinal,
			Kind:      kindOf(n),
			Level:     headingLevel(n),
			Quote:     quote(plain, 90),
			Hash:      hashText(plain),
			PlainText: plain,
		}
		// A mermaid fence is a diagram, not an opaque wall of source: its
		// statements anchor individually inside the fence's own markup.
		if fence, ok := n.(*ast.FencedCodeBlock); ok && isMermaidFence(fence, src) {
			p.blocks = append(p.blocks, block)
			at := len(p.blocks) - 1
			html, kids, ok := p.mermaidFence(fence, section, id)
			if ok {
				p.blocks[at].HTML = html
				p.blocks[at].HasChildren = kids
				continue
			}
			// Not a diagram type we take apart: render it as the code block
			// it is, with the anchor it already had.
			p.blocks = p.blocks[:at]
		}
		list, isList := n.(*ast.List)
		if !isList {
			var buf bytes.Buffer
			if err := md.Renderer().Render(&buf, src, n); err != nil {
				return nil, fmt.Errorf("render block %s: %w", id, err)
			}
			block.HTML = buf.String()
			p.blocks = append(p.blocks, block)
			continue
		}
		// The list is appended before its items so blocks stay in document
		// order; its markup is only known once the items are rendered.
		block.HasChildren = list.FirstChild() != nil
		p.blocks = append(p.blocks, block)
		at := len(p.blocks) - 1
		html, err := p.renderList(list, section, id, 1)
		if err != nil {
			return nil, fmt.Errorf("render block %s: %w", id, err)
		}
		p.blocks[at].HTML = html
	}
	return &Document{Path: path, Format: FormatMarkdown, Blocks: p.blocks}, nil
}
