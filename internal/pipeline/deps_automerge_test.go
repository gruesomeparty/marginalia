package pipeline_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// depsCheck runs scripts/deps-automerge-check.sh, the gate that lets a
// Dependabot PR merge itself into main on green CI.
func depsCheck(t *testing.T, in map[string]any) (string, int) {
	t.Helper()
	path := filepath.Join(filepath.Dir(script(t)), "deps-automerge-check.sh")
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", path)
	cmd.Stdin = bytes.NewReader(raw)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run: %v (stderr %q)", err, errOut.String())
		}
		code = exit.ExitCode()
	}
	return out.String() + errOut.String(), code
}

func commitBy(logins ...string) map[string]any {
	authors := make([]map[string]string, len(logins))
	for i, l := range logins {
		authors[i] = map[string]string{"login": l}
	}
	return map[string]any{"oid": "abc123", "authors": authors}
}

// update is a PR the gate should merge; each case below breaks one thing.
func update() map[string]any {
	return map[string]any{
		"pr": map[string]any{
			"number": 127, "author": map[string]any{"login": "app/dependabot", "is_bot": true},
			"baseRefName": "main", "headRefName": "dependabot/github_actions/github/codeql-action-4.39.0",
			"headRefOid": "abc123", "isDraft": false, "isCrossRepository": false,
			"commits": []any{commitBy("dependabot[bot]")},
		},
		"ci_sha": "abc123",
	}
}

func TestDepsAutomergeMergesADependabotPR(t *testing.T) {
	out, code := depsCheck(t, update())
	if code != 0 || !strings.Contains(out, "merge #127 (dependency update)") {
		t.Fatalf("exit=%d out=%q", code, out)
	}
}

func TestDepsAutomergeRefuses(t *testing.T) {
	cases := map[string]struct {
		mutate func(pr, in map[string]any)
		want   string
	}{
		// The exception is for dependency updates; a person's PR into main is
		// exactly what it must never reach.
		"human author":   {func(pr, _ map[string]any) { pr["author"] = map[string]any{"login": "gruesomeparty"} }, "not dependabot[bot]"},
		"other bot":      {func(pr, _ map[string]any) { pr["author"] = map[string]any{"login": "app/renovate"} }, "not dependabot[bot]"},
		"no author":      {func(pr, _ map[string]any) { delete(pr, "author") }, "not dependabot[bot]"},
		"human branch":   {func(pr, _ map[string]any) { pr["headRefName"] = "claude/storage-94" }, "not a dependabot/* branch"},
		"fork":           {func(pr, _ map[string]any) { pr["isCrossRepository"] = true }, "from a fork"},
		"other base":     {func(pr, _ map[string]any) { pr["baseRefName"] = "feature/hosted" }, "not main"},
		"draft":          {func(pr, _ map[string]any) { pr["isDraft"] = true }, "is a draft"},
		"stale CI":       {func(_, in map[string]any) { in["ci_sha"] = "old999" }, "CI passed on old999"},
		"no commits":     {func(pr, _ map[string]any) { pr["commits"] = []any{} }, "lists no commits"},
		"pushed to":      {func(pr, _ map[string]any) { pr["commits"] = []any{commitBy("dependabot[bot]"), commitBy("someone")} }, "commits by someone"},
		"co-authored":    {func(pr, _ map[string]any) { pr["commits"] = []any{commitBy("dependabot[bot]", "someone")} }, "commits by someone"},
		"no commit user": {func(pr, _ map[string]any) { pr["commits"] = []any{commitBy()} }, "no author on record"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := update()
			tc.mutate(in["pr"].(map[string]any), in)
			out, code := depsCheck(t, in)
			if code != 1 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit=%d, want 1 naming %q; out=%q", code, tc.want, out)
			}
		})
	}
}

func TestDepsAutomergeRejectsMalformedInput(t *testing.T) {
	if _, code := depsCheck(t, map[string]any{"pr": "nope"}); code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
}
