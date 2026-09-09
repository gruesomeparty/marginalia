package document

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// pathSep joins key paths for order lookups; it cannot occur in a TOML key.
const pathSep = "\x00"

// parseTOML reads TOML into an ordered node tree. The decoder hands back
// plain maps, so document order is recovered from the metadata's key list —
// a config reviewed out of order is a config reviewed badly.
func parseTOML(path string, src []byte) (*Document, error) {
	var raw map[string]any
	meta, err := toml.Decode(string(src), &raw)
	if err != nil {
		return nil, err
	}
	root := tomlValue(raw, "", tomlOrder(meta))
	return dataDocument(path, FormatTOML, []dataNode{root}), nil
}

// tomlOrder maps each table's key path to its keys in the order they appear
// in the source. Elements of an array of tables share one path, so their keys
// are ordered by first appearance across the array.
func tomlOrder(meta toml.MetaData) map[string][]string {
	order := map[string][]string{}
	seen := map[string]bool{}
	for _, key := range meta.Keys() {
		if len(key) == 0 {
			continue
		}
		parent := strings.Join(key[:len(key)-1], pathSep)
		name := key[len(key)-1]
		if seen[parent+pathSep+pathSep+name] {
			continue
		}
		seen[parent+pathSep+pathSep+name] = true
		order[parent] = append(order[parent], name)
	}
	return order
}

func tomlValue(v any, path string, order map[string][]string) dataNode {
	switch t := v.(type) {
	case map[string]any:
		n := dataNode{Kind: kindObject}
		for _, name := range tomlKeys(t, order[path]) {
			child := tomlValue(t[name], join(path, pathSep, name), order)
			child.Key, child.Keyed = name, true
			n.Children = append(n.Children, child)
		}
		return n
	case []map[string]any: // array of tables
		n := dataNode{Kind: kindArray}
		for _, elem := range t {
			n.Children = append(n.Children, tomlValue(elem, path, order))
		}
		return n
	case []any:
		n := dataNode{Kind: kindArray}
		for _, elem := range t {
			n.Children = append(n.Children, tomlValue(elem, path, order))
		}
		return n
	case string:
		return dataNode{Kind: kindString, Value: t}
	case bool:
		return dataNode{Kind: kindBool, Value: strconv.FormatBool(t)}
	case int64:
		return dataNode{Kind: kindNumber, Value: strconv.FormatInt(t, 10)}
	case float64:
		return dataNode{Kind: kindNumber, Value: strconv.FormatFloat(t, 'g', -1, 64)}
	case time.Time:
		return dataNode{Kind: "datetime", Value: t.Format(time.RFC3339)}
	case nil:
		return dataNode{Kind: kindNull, Value: "null"}
	}
	return dataNode{Kind: "scalar", Value: fmt.Sprint(v)}
}

// tomlKeys returns m's keys in source order, with anything the metadata did
// not mention appended alphabetically so no key can go missing from a review.
func tomlKeys(m map[string]any, ordered []string) []string {
	keys := make([]string, 0, len(m))
	placed := map[string]bool{}
	for _, name := range ordered {
		if _, ok := m[name]; ok && !placed[name] {
			keys = append(keys, name)
			placed[name] = true
		}
	}
	var rest []string
	for name := range m {
		if !placed[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}
