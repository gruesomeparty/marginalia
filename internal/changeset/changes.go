// Package changeset says what moved between two parses of a document: which
// blocks were added, edited or removed, and for an edited one, how its words
// changed (issue #214). A block is the same block when its id is; it changed
// when its hash did. That is the rule Materialize already applies to notes,
// so a block this package calls edited is one whose notes read as stale.
package changeset

import (
	"strings"
	"unicode"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// Kinds of change.
const (
	Added   = "added"
	Edited  = "edited"
	Removed = "removed"
)

// Change is one block that moved since the baseline.
type Change struct {
	Block string `json:"block"`
	Kind  string `json:"kind"`
	// Quote is the block's quote: the new one, or for a removed block the
	// last one it had, since there is nothing left on the page to read.
	Quote string `json:"quote"`
	// Redline is an edited block's text, word by word, against the old.
	Redline []Span `json:"redline,omitempty"`
}

// Span is a run of words that were kept ("="), deleted ("-") or inserted
// ("+"). Joined in order, the "=" and "+" spans are the new text and the
// "=" and "-" spans the old.
type Span struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Between lists what changed from old to now, in the order a reader meets
// it: now's blocks in document order, then what was removed. A nil old (no
// baseline yet) changes nothing.
//
// A container is left out when what changed inside it is already listed: a
// list whose item was edited, or an item whose list was added or removed
// with it. Otherwise one edit would be reported twice.
func Between(old, now *document.Document) []Change { return between(old, now, true) }

// Without is Between without the word-level redlines, for a caller that only
// counts or names the blocks: the redline is the expensive part.
func Without(old, now *document.Document) []Change { return between(old, now, false) }

func between(old, now *document.Document, redline bool) []Change {
	if old == nil || now == nil {
		return nil
	}
	before := make(map[string]document.Block, len(old.Blocks))
	for _, b := range old.Blocks {
		before[b.ID] = b
	}
	after := make(map[string]bool, len(now.Blocks))
	kind := map[string]string{}
	for _, b := range now.Blocks {
		after[b.ID] = true
		was, ok := before[b.ID]
		switch {
		case !ok:
			kind[b.ID] = Added
		case was.Hash != b.Hash:
			kind[b.ID] = Edited
		}
	}
	for _, b := range old.Blocks {
		if !after[b.ID] {
			kind[b.ID] = Removed
		}
	}
	// A container whose inline children moved is explained by them.
	explained := map[string]bool{}
	parent := parents(now, old)
	for id, k := range kind {
		if p := parent[id]; p != "" {
			if kind[p] == Edited {
				explained[p] = true
			}
			if kind[p] == k && k != Edited {
				explained[id] = true
			}
		}
	}
	var out []Change
	for _, b := range now.Blocks {
		k := kind[b.ID]
		if k == "" || explained[b.ID] {
			continue
		}
		c := Change{Block: b.ID, Kind: k, Quote: b.Quote}
		if k == Edited && redline {
			c.Redline = Words(before[b.ID].PlainText, b.PlainText)
		}
		out = append(out, c)
	}
	for _, b := range old.Blocks {
		if kind[b.ID] == Removed && !explained[b.ID] {
			out = append(out, Change{Block: b.ID, Kind: Removed, Quote: b.Quote})
		}
	}
	return out
}

// parents maps each inline block to its container, across both parses (the
// current one wins). Built once per call: a lookup per changed block that
// scanned every block would make a large, fully edited document quadratic.
func parents(docs ...*document.Document) map[string]string {
	out := map[string]string{}
	for i := len(docs) - 1; i >= 0; i-- {
		for _, b := range docs[i].Blocks {
			if b.Inline {
				out[b.ID] = b.Parent
			} else {
				out[b.ID] = ""
			}
		}
	}
	return out
}

// IDs splits a change list by kind, for a caller that only counts or lists.
func IDs(cs []Change) (added, edited, removed []string) {
	for _, c := range cs {
		switch c.Kind {
		case Added:
			added = append(added, c.Block)
		case Edited:
			edited = append(edited, c.Block)
		case Removed:
			removed = append(removed, c.Block)
		}
	}
	return added, edited, removed
}

// maxCells bounds the word diff's table. Two blocks of a few thousand words
// each would cost tens of millions of cells; past this the redline says the
// whole text was replaced, which is true enough to read.
const maxCells = 1 << 20

// Words diffs two texts word by word (a longest common subsequence over
// words, whitespace kept with the word before it), merging neighbouring
// spans of the same kind.
func Words(a, b string) []Span {
	x, y := tokens(a), tokens(b)
	var out []Span
	add := func(op, text string) {
		if text == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Op == op {
			out[n-1].Text += text
			return
		}
		out = append(out, Span{Op: op, Text: text})
	}
	// Common head and tail cost nothing and shrink the table.
	for len(x) > 0 && len(y) > 0 && x[0] == y[0] {
		add("=", x[0])
		x, y = x[1:], y[1:]
	}
	var tail []string
	for len(x) > 0 && len(y) > 0 && x[len(x)-1] == y[len(y)-1] {
		tail = append([]string{x[len(x)-1]}, tail...)
		x, y = x[:len(x)-1], y[:len(y)-1]
	}
	if (len(x)+1)*(len(y)+1) > maxCells {
		add("-", strings.Join(x, ""))
		add("+", strings.Join(y, ""))
	} else {
		lcs := make([][]int, len(x)+1)
		for i := range lcs {
			lcs[i] = make([]int, len(y)+1)
		}
		for i := len(x) - 1; i >= 0; i-- {
			for j := len(y) - 1; j >= 0; j-- {
				if x[i] == y[j] {
					lcs[i][j] = lcs[i+1][j+1] + 1
				} else {
					lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < len(x) && j < len(y) {
			switch {
			case x[i] == y[j]:
				add("=", x[i])
				i, j = i+1, j+1
			case lcs[i+1][j] >= lcs[i][j+1]:
				add("-", x[i])
				i++
			default:
				add("+", y[j])
				j++
			}
		}
		for ; i < len(x); i++ {
			add("-", x[i])
		}
		for ; j < len(y); j++ {
			add("+", y[j])
		}
	}
	for _, t := range tail {
		add("=", t)
	}
	return out
}

// tokens splits text into words, each carrying the whitespace after it, so
// joining them gives the text back.
func tokens(s string) []string {
	var out []string
	start, inSpace := 0, false
	for i, r := range s {
		sp := unicode.IsSpace(r)
		if inSpace && !sp {
			out = append(out, s[start:i])
			start = i
		}
		inSpace = sp
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
