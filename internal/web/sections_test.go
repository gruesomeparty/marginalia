package web

import (
	"slices"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/review"
)

func outlineOf(t *testing.T, name, src string) clientDoc {
	t.Helper()
	doc, err := document.ParseBytes(name, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return project(doc, &review.Config{ReadOnly: []string{"2/"}}, []string{name})
}

func labels(c clientDoc) []string {
	var out []string
	for _, s := range c.Sections {
		out = append(out, s.Label)
	}
	return out
}

// Every block lands in exactly one listed section, so the outline's totals are
// a regrouping of the header's and sum to them (issue #132).
func TestSectionsPartitionTheBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, file, src string
		want            []string
	}{
		{"h1 chapters", "a.md", "Intro.\n\n# One\n\nx\n\n## Sub\n\n- a\n- b\n\n# Two\n\nz\n", []string{"Before the first heading", "One", "Two"}},
		{"title over h2 chapters", "prd.md", "# PRD\n\nWhy.\n\n## Goals\n\ng\n\n## Scope\n\ns\n", []string{"PRD", "Goals", "Scope"}},
		{"h2 only", "b.md", "## A\n\na\n\n## B\n\nb\n", []string{"A", "B"}},
		{"json root", "c.json", `{"a":{"b":1},"c":[1,2]}`, []string{"$", "$.a", "$.c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := outlineOf(t, tc.file, tc.src)
			if got := labels(c); !slices.Equal(got, tc.want) {
				t.Errorf("sections = %q, want %q", got, tc.want)
			}
			listed := map[string]bool{}
			for _, s := range c.Sections {
				listed[s.ID] = true
			}
			perSection, commentable := 0, 0
			counts := map[string]int{}
			for _, b := range c.Blocks {
				if !listed[b.Section] {
					t.Errorf("block %s rolls up into %q, which the outline does not list", b.ID, b.Section)
				}
				if !b.ReadOnly {
					commentable++
					counts[b.Section]++
				}
			}
			for _, n := range counts {
				perSection += n
			}
			if perSection != commentable {
				t.Errorf("section totals sum to %d, the document has %d commentable blocks", perSection, commentable)
			}
		})
	}
}

// One section says nothing the progress bar does not, so there is no outline.
func TestNoOutlineForASingleSection(t *testing.T) {
	c := outlineOf(t, "a.md", "# Only\n\ntext\n\nmore\n")
	if c.Sections != nil {
		t.Errorf("sections = %q, want none", labels(c))
	}
	for _, b := range c.Blocks {
		if b.Section != "" {
			t.Errorf("block %s carries section %q with no outline", b.ID, b.Section)
		}
	}
}
