package reviewset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes files (relative path -> contents) under a fresh temp dir.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func rels(set *Set) []string {
	out := make([]string, 0, len(set.Docs))
	for _, d := range set.Docs {
		out = append(out, d.Rel)
	}
	return out
}

func TestLoadSingleFile(t *testing.T) {
	root := tree(t, map[string]string{"spec.md": "# Spec\n"})
	set, err := Load([]string{filepath.Join(root, "spec.md")})
	if err != nil {
		t.Fatal(err)
	}
	if !set.Single() || set.Docs[0].Rel != "spec.md" || set.Docs[0].Label != "spec.md" {
		t.Fatalf("set = %+v", set)
	}
	if set.Index != "" || set.Excluded != 0 {
		t.Errorf("a plain file review should have no index: %+v", set)
	}
}

// Several documents in one session is the point of the feature: the reviewer
// gets one server, not one port per file.
func TestLoadSeveralFiles(t *testing.T) {
	root := tree(t, map[string]string{"spec.md": "# S\n", "plan/plan.md": "# P\n", "api.proto": "message M { string a = 1; }\n"})
	set, err := Load([]string{
		filepath.Join(root, "spec.md"),
		filepath.Join(root, "plan", "plan.md"),
		filepath.Join(root, "api.proto"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rels(set), ","); got != "spec.md,plan/plan.md,api.proto" {
		t.Errorf("rels = %s, want argument order relative to the common root", got)
	}
	if set.Root != root {
		t.Errorf("root = %q, want %q", set.Root, root)
	}
}

func TestLoadDirectoryDiscovers(t *testing.T) {
	root := tree(t, map[string]string{
		"README.md":               "# R\n",
		"docs/plan.md":            "# P\n",
		"docs/deep/config.toml":   "a = 1\n",
		"notes.rst":               "unsupported\n",
		".hidden/secret.md":       "# nope\n",
		".dotfile.md":             "# nope\n",
		"node_modules/pkg/p.json": "{}\n",
		"vendor/dep/d.yaml":       "a: 1\n",
	})
	set, err := Load([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	want := "README.md,docs/deep/config.toml,docs/plan.md"
	if got := strings.Join(rels(set), ","); got != want {
		t.Errorf("rels = %s, want %s", got, want)
	}
}

func TestLoadUnsupportedFile(t *testing.T) {
	root := tree(t, map[string]string{"notes.rst": "x\n"})
	_, err := Load([]string{filepath.Join(root, "notes.rst")})
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedError", err)
	}
	if unsupported.Ext != ".rst" || !strings.Contains(unsupported.Error(), ".rst") {
		t.Errorf("unsupported = %+v", unsupported)
	}
	if e := (&UnsupportedError{Path: "x"}).Error(); !strings.Contains(e, "this file type") {
		t.Errorf("extensionless label = %q", e)
	}
}

func TestLoadMissingPathAndEmptyDirectory(t *testing.T) {
	if _, err := Load([]string{filepath.Join(t.TempDir(), "nope.md")}); err == nil {
		t.Error("expected an error for a missing path")
	}
	_, err := Load([]string{tree(t, map[string]string{"notes.rst": "x\n"})})
	if !errors.Is(err, ErrNoDocuments) {
		t.Errorf("err = %v, want ErrNoDocuments", err)
	}
}

func TestLoadDedupesRepeatedPaths(t *testing.T) {
	root := tree(t, map[string]string{"spec.md": "# S\n"})
	set, err := Load([]string{root, filepath.Join(root, "spec.md")})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Docs) != 1 {
		t.Errorf("a file named twice should appear once: %+v", rels(set))
	}
}

func TestLoadRefusesUnreviewablyLargeSet(t *testing.T) {
	files := map[string]string{}
	for i := 0; i <= maxDocs; i++ {
		files[fmt.Sprintf("doc%03d.md", i)] = "# x\n"
	}
	_, err := Load([]string{tree(t, files)})
	if err == nil || !strings.Contains(err.Error(), IndexName) {
		t.Fatalf("err = %v, want a cap error pointing at the index", err)
	}
}

// The index is a whitelist, an order and a set of labels in one.
func TestIndexCuratesTheSet(t *testing.T) {
	root := tree(t, map[string]string{
		"api/orders.proto": "message M { string a = 1; }\n",
		"docs/plan.md":     "# P\n",
		"docs/notes.md":    "# N\n",
		"README.md":        "# R\n",
		IndexName: "title: API contract review\n" +
			"docs:\n" +
			"  - path: api/orders.proto\n" +
			"    label: Order service contract\n" +
			"  - docs/plan.md\n",
	})
	set, err := Load([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rels(set), ","); got != "api/orders.proto,docs/plan.md" {
		t.Errorf("rels = %s, want the index's list in its order", got)
	}
	if set.Docs[0].Label != "Order service contract" {
		t.Errorf("label = %q, want the index's label", set.Docs[0].Label)
	}
	if set.Docs[1].Label != "docs/plan.md" {
		t.Errorf("unlabelled entry = %q, want its path", set.Docs[1].Label)
	}
	if set.Title != "API contract review" {
		t.Errorf("title = %q", set.Title)
	}
	if set.Index == "" {
		t.Error("set should record the index that shaped it")
	}
	// Two supported files (README.md, docs/notes.md) are invisible to the
	// reviewer; startup has to be able to say so.
	if set.Excluded != 2 {
		t.Errorf("excluded = %d, want 2", set.Excluded)
	}
}

// A mistyped whitelist entry would otherwise shrink the review silently.
func TestIndexRejectsBadEntries(t *testing.T) {
	cases := map[string]struct{ index, want string }{
		"missing file": {"docs:\n  - docs/gone.md\n", "gone.md"},
		"outside root": {"docs:\n  - ../elsewhere.md\n", "outside"},
		"no path":      {"docs:\n  - label: nameless\n", "no path"},
		"empty list":   {"title: nothing\n", "lists no documents"},
		"not yaml":     {"docs: [oh: no\n", IndexName},
		"unsupported":  {"docs:\n  - notes.rst\n", ".rst"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := tree(t, map[string]string{
				"docs/plan.md": "# P\n",
				"notes.rst":    "x\n",
				IndexName:      c.index,
			})
			_, err := Load([]string{root})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}
