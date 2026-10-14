package storage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every append to persistent state goes through this package, and this test
// is what keeps it that way: O_APPEND is the one primitive an appender needs,
// so a Go file anywhere else that opens a file with it is a second log writer
// — which is exactly the thing a storage interface exists to make impossible.
// The same goes for the other ways of putting bytes on disk that a log or a
// blob would need; the document, the diagram cache and `export`'s page are
// listed as the exceptions they are.
func TestNoAppenderOutsideStorage(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]bool{
		"internal/diagram/diagram.go": true, // the SVG cache: disposable, not state
		"cmd/export.go":               true, // the review page, which is output, not a log
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "site" || (name == "storage" && filepath.Base(filepath.Dir(path)) == "internal") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, needle := range []string{"O_APPEND", "os.WriteFile(", "os.Create("} {
			if strings.Contains(string(src), needle) && !allowed[rel] {
				t.Errorf("%s uses %s — persistent state is written through internal/storage only", rel, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}
