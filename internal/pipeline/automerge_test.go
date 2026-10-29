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

// check runs scripts/automerge-check.sh, the gate that decides whether a
// pipeline PR may merge itself into its integration branch.
func check(t *testing.T, in map[string]any) (string, int) {
	t.Helper()
	path := filepath.Join(filepath.Dir(script(t)), "automerge-check.sh")
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

func labels(names ...string) []map[string]string {
	out := make([]map[string]string, len(names))
	for i, n := range names {
		out[i] = map[string]string{"name": n}
	}
	return out
}

// eligible is a PR the gate should merge; each case below breaks one thing.
func eligible() map[string]any {
	return map[string]any{
		"pr": map[string]any{
			"number": 120, "baseRefName": "feature/hosted", "headRefName": "claude/storage-94",
			"headRefOid": "abc123", "isDraft": false, "isCrossRepository": false,
			"body": "Closes #94.\n\nAcceptance criterion: …",
		},
		"issue":  map[string]any{"number": 94, "state": "OPEN", "labels": labels("enhancement", "approved-for-agent", "base:feature/hosted")},
		"ci_sha": "abc123",
	}
}

func TestAutomergeMergesAnEligiblePR(t *testing.T) {
	out, code := check(t, eligible())
	if code != 0 || !strings.Contains(out, "merge #120 closes #94 into feature/hosted") {
		t.Fatalf("exit=%d out=%q", code, out)
	}
}

func TestAutomergeRefuses(t *testing.T) {
	cases := map[string]struct {
		mutate func(pr, issue map[string]any, in map[string]any)
		want   string
	}{
		// The rule the whole gate exists for.
		"main":             {func(pr, _, _ map[string]any) { pr["baseRefName"] = "main" }, "targets main, which only a human merges"},
		"other base":       {func(pr, _, _ map[string]any) { pr["baseRefName"] = "release/1" }, "not a feature/* integration branch"},
		"traversal":        {func(pr, _, _ map[string]any) { pr["baseRefName"] = "feature/../main" }, "not a feature/*"},
		"human branch":     {func(pr, _, _ map[string]any) { pr["headRefName"] = "berkay/wip" }, "not a claude/* pipeline branch"},
		"fork":             {func(pr, _, _ map[string]any) { pr["isCrossRepository"] = true }, "from a fork"},
		"draft":            {func(pr, _, _ map[string]any) { pr["isDraft"] = true }, "is a draft"},
		"review blocking":  {func(pr, _, _ map[string]any) { pr["labels"] = labels("review:blocking") }, "labelled review:blocking"},
		"stale CI":         {func(_, _, in map[string]any) { in["ci_sha"] = "old999" }, "CI passed on old999"},
		"no closes":        {func(pr, _, _ map[string]any) { pr["body"] = "Implements the storage layer." }, "names no issue"},
		"two issues":       {func(pr, _, _ map[string]any) { pr["body"] = "Closes #94, fixes #95" }, "closes several issues"},
		"wrong issue":      {func(_, issue, _ map[string]any) { issue["number"] = 93 }, "the issue checked was #93"},
		"closed issue":     {func(_, issue, _ map[string]any) { issue["state"] = "CLOSED" }, "is not open"},
		"unapproved":       {func(_, issue, _ map[string]any) { issue["labels"] = labels("base:feature/hosted") }, "not approved-for-agent"},
		"other base label": {func(_, issue, _ map[string]any) { issue["labels"] = labels("approved-for-agent", "base:feature/other") }, "not labelled base:feature/hosted"},
		"no base label":    {func(_, issue, _ map[string]any) { issue["labels"] = labels("approved-for-agent") }, "not labelled base:feature/hosted"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := eligible()
			tc.mutate(in["pr"].(map[string]any), in["issue"].(map[string]any), in)
			out, code := check(t, in)
			if code != 1 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit=%d, want 1 naming %q; out=%q", code, tc.want, out)
			}
		})
	}
}

func TestAutomergeRejectsMalformedInput(t *testing.T) {
	if _, code := check(t, map[string]any{"pr": "nope"}); code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
}
