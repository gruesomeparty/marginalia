// Package reviewset resolves what a `marginalia serve` invocation is asking a
// human to review: one file, several files, or a directory of them — with an
// optional `.marginalia.yml` index curating which documents appear, in what
// order, under what names.
package reviewset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// IndexName is the per-directory index file that curates a review set.
const IndexName = ".marginalia.yml"

// maxDocs caps a discovered set. A review nobody can finish is not a review;
// past this many documents the reviewer needs an index to say what matters.
const maxDocs = 200

// Doc is one document in a review set.
type Doc struct {
	Path  string // path as it will be opened and as feedback events record it
	Rel   string // path relative to the set root; also the document's URL
	Label string // what the reviewer sees in the navigation tree
}

// Set is the collection of documents one server session serves.
type Set struct {
	Root     string // directory the documents' Rel paths are relative to
	Title    string // session title from an index, if any
	Index    string // path of the index that shaped this set, "" when discovered
	Docs     []Doc
	Excluded int // supported files under Root that the index left out
}

// Single reports whether this is a plain one-document review.
func (s *Set) Single() bool { return len(s.Docs) == 1 }

// ErrNoDocuments means nothing under the given paths can be reviewed yet.
var ErrNoDocuments = errors.New("no supported documents found")

// UnsupportedError names a file whose format Marginalia cannot render, so the
// CLI can answer with the request-feature pointer.
type UnsupportedError struct {
	Path string
	Ext  string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%s: %s is not a supported format", e.Path, e.label())
}

func (e *UnsupportedError) label() string {
	if e.Ext == "" {
		return "this file type"
	}
	return e.Ext
}

// Load resolves CLI arguments into a review set. Each argument is either a
// document or a directory to search; a directory carrying an index is shaped
// by that index alone.
func Load(args []string) (*Set, error) {
	var (
		docs      []Doc
		index     string
		indexRoot string
		title     string
		exclude   int
	)
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if supportedFormat(arg) == "" {
				return nil, &UnsupportedError{Path: arg, Ext: strings.ToLower(filepath.Ext(arg))}
			}
			docs = append(docs, Doc{Path: arg})
			continue
		}
		dir, err := loadDir(arg)
		if err != nil {
			return nil, err
		}
		docs = append(docs, dir.Docs...)
		exclude += dir.Excluded
		if dir.Index != "" && index == "" {
			index, title, indexRoot = dir.Index, dir.Title, dir.Root
		}
	}
	docs = dedupe(docs)
	if len(docs) == 0 {
		return nil, fmt.Errorf("%w under %s", ErrNoDocuments, strings.Join(args, ", "))
	}
	if len(docs) > maxDocs {
		return nil, fmt.Errorf("%d supported documents found under %s — narrow the paths or add a %s index naming the ones to review",
			len(docs), strings.Join(args, ", "), IndexName)
	}
	// A curated set is rooted where its index lives, so exclusion counts and
	// document URLs are relative to the same directory the index describes.
	root := indexRoot
	if root == "" {
		root = commonRoot(docs)
	}
	set := &Set{Root: root, Title: title, Index: index, Docs: docs, Excluded: exclude}
	label(set)
	return set, nil
}

// loadDir resolves one directory: its index if it has one, otherwise every
// supported document beneath it.
func loadDir(dir string) (*Set, error) {
	found, err := walk(dir)
	if err != nil {
		return nil, err
	}
	indexPath := filepath.Join(dir, IndexName)
	if _, err := os.Stat(indexPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		return &Set{Root: dir, Docs: found}, nil
	}
	idx, err := readIndex(indexPath, dir)
	if err != nil {
		return nil, err
	}
	// The index is the whole tree, so anything it omits is invisible to the
	// reviewer — count it so startup can say so out loud.
	listed := map[string]bool{}
	for _, d := range idx.Docs {
		listed[filepath.Clean(d.Path)] = true
	}
	excluded := 0
	for _, d := range found {
		if !listed[filepath.Clean(d.Path)] {
			excluded++
		}
	}
	idx.Excluded = excluded
	return idx, nil
}

// walk collects every supported document under dir, in path order, skipping
// hidden and dependency directories — a review set is authored content, not a
// checkout.
func walk(dir string) ([]Doc, error) {
	var docs []Doc
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || supportedFormat(path) == "" {
			return nil
		}
		docs = append(docs, Doc{Path: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

// supportedFormat is empty when Marginalia has no renderer for the file.
func supportedFormat(path string) string { return document.FormatFor(path) }

func dedupe(docs []Doc) []Doc {
	seen := map[string]bool{}
	out := make([]Doc, 0, len(docs))
	for _, d := range docs {
		key, err := filepath.Abs(d.Path)
		if err != nil {
			key = filepath.Clean(d.Path)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// commonRoot is the deepest directory containing every document, so Rel paths
// stay as short as the set allows.
func commonRoot(docs []Doc) string {
	root := filepath.Dir(docs[0].Path)
	for _, d := range docs[1:] {
		root = sharedPrefix(root, filepath.Dir(d.Path))
	}
	if root == "" {
		return "."
	}
	return root
}

func sharedPrefix(a, b string) string {
	sep := string(filepath.Separator)
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return a
	}
	as, bs := strings.Split(a, sep), strings.Split(b, sep)
	var shared []string
	for i := 0; i < len(as) && i < len(bs) && as[i] == bs[i]; i++ {
		shared = append(shared, as[i])
	}
	switch {
	case len(shared) == 0:
		return ""
	case len(shared) == 1 && shared[0] == "": // only the filesystem root in common
		return sep
	}
	// strings.Join, not filepath.Join: the latter drops the leading empty
	// element of an absolute path and would quietly make the root relative.
	return strings.Join(shared, sep)
}

// label fills in each document's Rel and, where the index didn't name it, its
// display label.
func label(s *Set) {
	for i := range s.Docs {
		d := &s.Docs[i]
		rel, err := filepath.Rel(s.Root, d.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = filepath.Base(d.Path)
		}
		d.Rel = filepath.ToSlash(rel)
		if d.Label == "" {
			d.Label = d.Rel
		}
	}
}
