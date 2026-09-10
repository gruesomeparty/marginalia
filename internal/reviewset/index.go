package reviewset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// index is the on-disk shape of a .marginalia.yml file:
//
//	title: API contract review
//	docs:
//	  - path: api/orders.proto
//	    label: Order service contract
//	  - docs/plan.md
//
// It is a whitelist, an order, and a set of names in one — the agent handing
// work over says "review these, in this order, under these labels" instead of
// exposing whatever the directory happens to contain.
type index struct {
	Title string       `yaml:"title"`
	Docs  []indexEntry `yaml:"docs"`
}

type indexEntry struct {
	Path  string `yaml:"path"`
	Label string `yaml:"label"`
}

// UnmarshalYAML accepts either a mapping or a bare path string, so a set that
// needs no custom labels stays a plain list.
func (e *indexEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&e.Path)
	}
	type raw indexEntry
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*e = indexEntry(r)
	return nil
}

// readIndex loads an index and resolves its entries against dir. Every listed
// path must exist and be reviewable: a typo in a whitelist would otherwise
// shrink the review silently, which is the one failure this file must not have.
func readIndex(path, dir string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(idx.Docs) == 0 {
		return nil, fmt.Errorf("%s: lists no documents", path)
	}
	set := &Set{Root: dir, Title: idx.Title, Index: path}
	for _, entry := range idx.Docs {
		if strings.TrimSpace(entry.Path) == "" {
			return nil, fmt.Errorf("%s: an entry has no path", path)
		}
		full := filepath.Join(dir, entry.Path)
		rel, err := filepath.Rel(dir, full)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%s: %q is outside %s", path, entry.Path, dir)
		}
		if _, err := os.Stat(full); err != nil {
			return nil, fmt.Errorf("%s: %q is listed but %w", path, entry.Path, err)
		}
		if supportedFormat(full) == "" {
			return nil, &UnsupportedError{Path: full, Ext: strings.ToLower(filepath.Ext(full))}
		}
		set.Docs = append(set.Docs, Doc{Path: full, Label: entry.Label})
	}
	return set, nil
}
