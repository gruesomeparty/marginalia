package document

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseYAML reads YAML into an ordered node tree. yaml.Node keeps both key
// order and the author's comments, and the comments are half of why a YAML
// config is reviewable at all — they say why a value is what it is.
func parseYAML(path string, src []byte) (*Document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var roots []dataNode
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		content := &doc
		if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
			content = doc.Content[0]
		}
		n := yamlNode(content)
		n.Comment = joinText(commentText(doc.HeadComment), n.Comment)
		roots = append(roots, n)
	}
	if len(roots) == 0 {
		return nil, errors.New("no YAML documents found")
	}
	return dataDocument(path, FormatYAML, roots), nil
}

func yamlNode(y *yaml.Node) dataNode {
	n := dataNode{Comment: commentText(y.HeadComment), Trailing: commentText(y.LineComment)}
	switch y.Kind {
	case yaml.MappingNode:
		n.Kind = kindObject
		for i := 0; i+1 < len(y.Content); i += 2 {
			key, val := y.Content[i], y.Content[i+1]
			child := yamlNode(val)
			child.Key, child.Keyed = key.Value, true
			// A mapping entry's comments hang off its key, not its value.
			child.Comment = joinText(commentText(key.HeadComment), child.Comment)
			child.Trailing = joinText(commentText(key.LineComment), child.Trailing)
			n.Children = append(n.Children, child)
		}
	case yaml.SequenceNode:
		n.Kind = kindArray
		for _, item := range y.Content {
			n.Children = append(n.Children, yamlNode(item))
		}
	case yaml.AliasNode:
		n.Kind, n.Value = "alias", "*"+y.Value
	default:
		n.Kind, n.Value = yamlKind(y.Tag), y.Value
	}
	return n
}

// yamlKind maps a resolved YAML tag onto the kinds the tree renderer knows,
// keeping any exotic tag (`!!timestamp`, a custom one) as its own label.
func yamlKind(tag string) string {
	switch tag {
	case "!!str", "":
		return kindString
	case "!!int", "!!float":
		return kindNumber
	case "!!bool":
		return kindBool
	case "!!null":
		return kindNull
	}
	return strings.TrimPrefix(tag, "!!")
}

// commentText strips comment markers, leaving the prose.
func commentText(s string) string {
	if s == "" {
		return ""
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, " ")
}

func joinText(parts ...string) string { return strings.Join(nonEmpty(parts...), " ") }
