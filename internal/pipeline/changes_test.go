package pipeline_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ciChanges runs scripts/ci-changes.sh with paths on stdin (and args) and
// returns the keys it printed as true.
func ciChanges(t *testing.T, paths []string, args ...string) map[string]bool {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(filepath.Dir(script(t)), "ci-changes.sh")}, args...)...)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-changes %v: %v (%s)", paths, err, out)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || (v != "true" && v != "false") {
			t.Fatalf("ci-changes printed %q", line)
		}
		got[k] = v == "true"
	}
	if len(got) != 8 {
		t.Fatalf("ci-changes printed %d keys, want 8: %s", len(got), out)
	}
	return got
}

func TestCIChanges(t *testing.T) {
	everything := "test go image cloudflare workflows deps codeql"
	for _, c := range []struct {
		name  string
		paths []string
		want  string // the keys that are true, space-separated
	}{
		{"readme", []string{"README.md"}, "docs_only"},
		{"docs", []string{"docs/hosting.md", "CHANGELOG.md"}, "docs_only"},
		// Prose that steers agents runs no CI, but keeps its agent review.
		{"claude.md", []string{"CLAUDE.md"}, ""},
		{"a skill", []string{"skills/review-pr/SKILL.md"}, ""},
		{"prd", []string{"PRD.md"}, ""},
		{"a plugin command", []string{"commands/review-doc.md"}, ""},
		{"homepage", []string{"site/index.html"}, ""},
		// Tests read testdata, Markdown included.
		{"testdata", []string{"internal/document/testdata/list.md"}, "test go image"},
		{"go", []string{"internal/web/page.go"}, "test go image codeql"},
		{"embedded template", []string{"internal/web/review.html.tmpl"}, "test go image"},
		{"module", []string{"go.sum"}, "test go image deps codeql"},
		{"template", []string{"deploy/cloudflare/src/index.ts"}, "test cloudflare"},
		{"template lockfile", []string{"deploy/cloudflare/package-lock.json"}, "test cloudflare deps"},
		{"dockerfile", []string{"Dockerfile"}, "test image"},
		{"a loop workflow", []string{".github/workflows/review-prs.yml"}, "test workflows"},
		{"a script", []string{"scripts/merge-gate.sh"}, "test"},
		{"docs and code", []string{"README.md", "cmd/serve.go"}, "test go image codeql"},
		// A rename names both sides; the old one counts too.
		{"renamed out of testdata", []string{"docs/a.md", "internal/x/testdata/a.md"}, "test go image"},
		// CI itself, or nothing to go on: everything.
		{"ci workflow", []string{"README.md", ".github/workflows/ci.yml"}, everything},
		{"codeql workflow", []string{".github/workflows/codeql.yml"}, everything},
		{"this script", []string{"scripts/ci-changes.sh"}, everything},
		{"no paths", nil, everything},
	} {
		got := ciChanges(t, c.paths)
		want := map[string]bool{}
		for _, k := range strings.Fields(c.want) {
			want[k] = true
		}
		for k, v := range got {
			if v != want[k] {
				t.Errorf("%s: %s=%v, want %v", c.name, k, v, want[k])
			}
		}
	}
	for k, v := range ciChanges(t, nil, "--all") {
		if v != (k != "docs_only") {
			t.Errorf("--all: %s=%v", k, v)
		}
	}
}
