package pipeline_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type reviewEdits struct {
	Number  int      `json:"number"`
	Verdict string   `json:"verdict"`
	Add     []string `json:"add"`
	Remove  []string `json:"remove"`
	Comment string   `json:"comment"`
}

const reviewed = "0123456789abcdef0123456789abcdef01234567"

// review runs scripts/apply-review.sh, which decides what an adversarial
// review may change on a PR. The agent that wrote the verdict read a diff
// anyone could have written, so this script is the boundary, not the prompt.
func review(t *testing.T, in map[string]any) (reviewEdits, string, int) {
	t.Helper()
	path := filepath.Join(filepath.Dir(script(t)), "apply-review.sh")
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
	var e reviewEdits
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &e); err != nil {
			t.Fatalf("output is not JSON: %v\n%s", err, out.String())
		}
	}
	return e, errOut.String(), code
}

func reviewInput(v map[string]any, have ...string) map[string]any {
	return map[string]any{
		"pr":           map[string]any{"number": 160, "headRefOid": reviewed, "labels": labels(have...)},
		"review":       v,
		"reviewed_sha": reviewed,
		"now":          "2026-10-29",
	}
}

func finding(severity, title string) map[string]any {
	return map[string]any{"severity": severity, "file": "internal/server/feedback.go", "line": 88, "title": title, "detail": "Why, and the input that shows it."}
}

func TestReviewLabelFollowsTheWorstFinding(t *testing.T) {
	cases := map[string]struct {
		findings []any
		want     string
	}{
		"none":     {nil, "pass"},
		"minor":    {[]any{finding("minor", "a")}, "pass"},
		"major":    {[]any{finding("minor", "a"), finding("major", "b")}, "concerns"},
		"blocking": {[]any{finding("major", "b"), finding("blocking", "c")}, "blocking"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// The agent's own word for the verdict is not consulted.
			v := map[string]any{"pr": 160, "head_sha": reviewed, "verdict": "pass", "summary": "s", "findings": tc.findings}
			e, errOut, code := review(t, reviewInput(v, "review:pass", "review:blocking", "bug"))
			if code != 0 {
				t.Fatalf("exit=%d: %s", code, errOut)
			}
			label := "review:" + tc.want
			if e.Verdict != tc.want || !strings.Contains(e.Comment, "**"+tc.want+"**") {
				t.Fatalf("verdict=%q, want %q; comment:\n%s", e.Verdict, tc.want, e.Comment)
			}
			if wantAdd := label != "review:pass" && label != "review:blocking"; wantAdd != slices.Contains(e.Add, label) {
				t.Fatalf("add=%v for %s", e.Add, label)
			}
			var want []string
			for _, l := range []string{"review:pass", "review:blocking"} {
				if l != label {
					want = append(want, l)
				}
			}
			if !slices.Equal(e.Remove, want) {
				t.Fatalf("remove=%v, want %v: the other review labels and nothing else", e.Remove, want)
			}
		})
	}
}

func TestReviewCommentIsMarkedAndOrdered(t *testing.T) {
	v := map[string]any{"summary": "Adds a route.", "findings": []any{finding("minor", "small"), finding("blocking", "big")}, "agent_notes": "checked X"}
	e, errOut, code := review(t, reviewInput(v))
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, errOut)
	}
	// The workflow finds the comment to edit by this prefix, and the sweep
	// finds the reviewed head by it.
	if !strings.HasPrefix(e.Comment, "<!-- agent:review v1\nsha: "+reviewed+"\n") {
		t.Fatalf("comment does not start with the marker:\n%s", e.Comment)
	}
	if strings.Index(e.Comment, "big") > strings.Index(e.Comment, "small") {
		t.Fatalf("blocking finding is not listed first:\n%s", e.Comment)
	}
	if !strings.Contains(e.Comment, "`internal/server/feedback.go:88`") || !strings.Contains(e.Comment, "of 0123456") {
		t.Fatalf("comment does not name the place or the commit:\n%s", e.Comment)
	}
	if hidden := e.Comment[:strings.Index(e.Comment, "-->")]; !strings.Contains(hidden, "notes: checked X") {
		t.Fatalf("agent notes are not in the hidden block:\n%s", e.Comment)
	}
}

func TestReviewTextIsInert(t *testing.T) {
	v := map[string]any{
		"summary": "cc @gruesomeparty, see [this](https://example.invalid) --> <!-- hidden",
		"findings": []any{map[string]any{
			"severity": "major", "file": "a.go`; echo", "line": 3.7,
			"title": "t @someone", "detail": strings.Repeat("x", 2000),
		}},
		"agent_notes": "a --> b",
	}
	e, errOut, code := review(t, reviewInput(v))
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, errOut)
	}
	if strings.Count(e.Comment, "-->") != 1 || strings.Count(e.Comment, "<!--") != 1 {
		t.Fatalf("text opened or closed an HTML comment:\n%s", e.Comment)
	}
	for _, bad := range []string{"@gruesomeparty", "@someone", "](https", "a.go`; echo", ":3.7", strings.Repeat("x", 700)} {
		if strings.Contains(e.Comment, bad) {
			t.Fatalf("comment contains %q:\n%s", bad, e.Comment)
		}
	}
	if !strings.Contains(e.Comment, "`a.goecho:3`") {
		t.Fatalf("path not reduced to a plain path:\n%s", e.Comment)
	}
}

func TestReviewRefusesAMovedHead(t *testing.T) {
	in := reviewInput(map[string]any{"findings": []any{}})
	in["pr"].(map[string]any)["headRefOid"] = strings.Repeat("f", 40)
	if _, errOut, code := review(t, in); code != 3 || !strings.Contains(errOut, "moved") {
		t.Fatalf("exit=%d, want 3 for a head the PR moved past: %s", code, errOut)
	}
}

func TestReviewRefusesAnInvalidVerdict(t *testing.T) {
	many := make([]any, 21)
	for i := range many {
		many[i] = finding("minor", "n")
	}
	cases := map[string]map[string]any{
		"severity":     {"findings": []any{finding("critical", "x")}},
		"no severity":  {"findings": []any{map[string]any{"title": "x"}}},
		"no title":     {"findings": []any{map[string]any{"severity": "minor"}}},
		"not a list":   {"findings": "none"},
		"too many":     {"findings": many},
		"other PR":     {"pr": 161},
		"other commit": {"head_sha": strings.Repeat("e", 40)},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			if _, errOut, code := review(t, reviewInput(v)); code != 1 {
				t.Fatalf("exit=%d, want 1: %s", code, errOut)
			}
		})
	}
}

func TestReviewRejectsMalformedInput(t *testing.T) {
	for name, in := range map[string]map[string]any{
		"no review": {"pr": map[string]any{"number": 1}, "reviewed_sha": reviewed},
		"bad sha":   {"pr": map[string]any{"number": 1, "headRefOid": "abc"}, "review": map[string]any{}, "reviewed_sha": "abc"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, code := review(t, in); code != 2 {
				t.Fatalf("exit=%d, want 2", code)
			}
		})
	}
}
