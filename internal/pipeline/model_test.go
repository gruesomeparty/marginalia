package pipeline_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pickModel runs scripts/pick-model.sh for loop with in on stdin.
func pickModel(t *testing.T, loop, in string) string {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(filepath.Dir(script(t)), "pick-model.sh"), loop)
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pick-model %s: %v (%s)", loop, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPickModel(t *testing.T) {
	const (
		haiku  = "claude-haiku-5-5"
		sonnet = "claude-sonnet-5-5"
		opus   = "claude-opus-5-5"
	)
	for _, c := range []struct{ name, loop, in, want string }{
		{"triage", "triage", "", haiku},
		{"small review", "review", `{"labels":[],"files":[{"path":"internal/web/page.go","additions":20,"deletions":5}]}`, sonnet},
		{"large review", "review", `{"labels":[],"files":[{"path":"internal/web/page.go","additions":250,"deletions":60}]}`, opus},
		{"long docs review", "review", `{"labels":[],"files":[{"path":"README.md","additions":400,"deletions":0},{"path":"internal/x/testdata/a.json","additions":400,"deletions":0}]}`, sonnet},
		{"long skill review", "review", `{"labels":[],"files":[{"path":"skills/review-pr/SKILL.md","additions":400,"deletions":0}]}`, opus},
		{"design-heavy review", "review", `{"labels":[],"files":[{"path":"internal/storage/r2.go","additions":3,"deletions":1}]}`, opus},
		{"deploy review", "review", `{"files":[{"path":"deploy/cloudflare/src/index.ts","additions":1,"deletions":1}]}`, opus},
		{"mcp review", "review", `{"files":[{"path":"internal/mcpserver/tools.go","additions":1,"deletions":0}]}`, opus},
		{"truncated listing", "review", `{"files":[` + strings.Repeat(`{"path":"a","additions":0,"deletions":1},`, 99) + `{"path":"a","additions":0,"deletions":1}]}`, opus},
		{"label up", "review", `{"labels":[{"name":"model:opus"}],"files":[]}`, opus},
		{"label down", "review", `{"labels":[{"name":"model:sonnet"}],"files":[{"path":"internal/storage/r2.go","additions":900,"deletions":0}]}`, sonnet},
		{"small issue", "implement", `{"labels":[{"name":"size:s"}]}`, sonnet},
		{"medium issue", "implement", `{"labels":[{"name":"size:m"}]}`, opus},
		{"unknown issue", "implement", "", opus},
		{"issue label", "implement", `{"labels":[{"name":"size:l"},{"name":"model:haiku"}]}`, haiku},
	} {
		if got := pickModel(t, c.loop, c.in); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
