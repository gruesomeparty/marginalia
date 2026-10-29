// Package pipeline_test covers scripts/approved-queue.sh, the triage gate the
// automated implementation pipeline runs before it touches any code. The gate
// is a shell script (it consumes `gh issue list --json` output), so the test
// drives the real script rather than a reimplementation of it.
package pipeline_test

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func script(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "scripts", "approved-queue.sh")
}

// run returns the gate's stdout, stderr and exit code for the given queue JSON.
func run(t *testing.T, queue string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("bash", script(t))
	cmd.Stdin = strings.NewReader(queue)
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
	return out.String(), errOut.String(), code
}

// The queue is worked oldest first, and only issues that say what was expected
// and what "done" means are eligible.
func TestQueueNamesOldestReadyIssue(t *testing.T) {
	out, _, code := run(t, `[
	  {"number": 26, "title": "Vague ask", "body": "It should be better."},
	  {"number": 12, "title": "List items", "body": "Desired: expected per-item anchors.\nAcceptance criterion: anchored."},
	  {"number": 9,  "title": "Tech debt", "body": "Expected behavior: cleanups.\nAcceptance criterion: tests pass."}
	]`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "next: #9") {
		t.Errorf("gate should pick the oldest ready issue:\n%s", out)
	}
	if !strings.Contains(out, "#12    READY") || !strings.Contains(out, "#26    NOT READY") {
		t.Errorf("verdicts wrong:\n%s", out)
	}
	// It has to say what is missing, or the pipeline cannot ask for it.
	if !strings.Contains(out, "missing: expected behaviour, acceptance criterion") {
		t.Errorf("gate should name what is missing:\n%s", out)
	}
	// Order is the queue order, oldest first.
	if strings.Index(out, "#9") > strings.Index(out, "#12") {
		t.Errorf("queue not ordered oldest first:\n%s", out)
	}
}

// An issue missing its acceptance criterion is not implementable from the
// issue, so the run stops rather than guessing what to build.
func TestQueueStopsWhenNothingIsReady(t *testing.T) {
	out, errOut, code := run(t, `[{"number": 26, "title": "Vague", "body": "expected: better"}]`)
	if code != 3 {
		t.Fatalf("exit=%d, want 3\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "missing: acceptance criterion") {
		t.Errorf("should name the missing criterion:\n%s", out)
	}
	if !strings.Contains(errOut, "need triage") {
		t.Errorf("stderr should explain the stop: %q", errOut)
	}
}

func TestQueueEmptyIsNotAFailure(t *testing.T) {
	out, _, code := run(t, `[]`)
	if code != 0 || !strings.Contains(out, "queue empty") {
		t.Fatalf("exit=%d out=%q", code, out)
	}
}

func TestQueueRejectsUnexpectedInput(t *testing.T) {
	_, errOut, code := run(t, `{"not": "an array"}`)
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if !strings.Contains(errOut, "JSON array") {
		t.Errorf("stderr should say what it wanted: %q", errOut)
	}
}

// A body-less issue must not crash the gate — `gh` omits empty bodies.
func TestQueueHandlesMissingBody(t *testing.T) {
	out, _, code := run(t, `[{"number": 30, "title": "No body"}]`)
	if code != 3 {
		t.Fatalf("exit=%d, want 3\n%s", code, out)
	}
	if !strings.Contains(out, "#30    NOT READY") {
		t.Errorf("verdict wrong:\n%s", out)
	}
}

const ready = `"body": "Expected behaviour: x.\nAcceptance criterion: y."`

// An issue names its integration branch with a base: label, and the gate hands
// that branch to the run; no label means main.
func TestQueueNamesTheBaseBranch(t *testing.T) {
	out, _, code := run(t, `[
	  {"number": 5, "title": "Hosted", `+ready+`, "labels": [{"name": "approved-for-agent"}, {"name": "base:feature/hosted"}]},
	  {"number": 7, "title": "Plain", `+ready+`, "labels": [{"name": "approved-for-agent"}]}
	]`)
	if code != 0 {
		t.Fatalf("exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "next: #5\nbase: feature/hosted") {
		t.Errorf("gate should name the next issue's base:\n%s", out)
	}
	if !strings.Contains(out, "#7     READY      Plain  -> main") {
		t.Errorf("an unlabelled issue targets main:\n%s", out)
	}
}

func TestQueueDefaultsToMainWithoutLabels(t *testing.T) {
	out, _, code := run(t, `[{"number": 9, "title": "Old shape", `+ready+`}]`)
	if code != 0 || !strings.Contains(out, "next: #9\nbase: main") {
		t.Fatalf("exit=%d\n%s", code, out)
	}
}

// Only main and feature/* are bases: a PR into feature/* auto-merges, one into
// main waits for a human, and an issue that is ambiguous about which it is
// must not be guessed at.
func TestQueueRefusesAmbiguousOrForeignBases(t *testing.T) {
	for name, tc := range map[string]struct{ labels, want string }{
		"two bases":      {`[{"name": "base:feature/a"}, {"name": "base:feature/b"}]`, "a single base label (has: feature/a,feature/b)"},
		"not a feature":  {`[{"name": "base:release"}]`, "a valid base (main or feature/*, got: release)"},
		"path traversal": {`[{"name": "base:feature/../main"}]`, "a valid base"},
		"bare prefix":    {`[{"name": "base:feature/"}]`, "a valid base"},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, code := run(t, `[{"number": 4, "title": "T", `+ready+`, "labels": `+tc.labels+`}]`)
			if code != 3 {
				t.Fatalf("exit=%d, want 3\n%s", code, out)
			}
			if !strings.Contains(out, "#4     NOT READY") || !strings.Contains(out, tc.want) {
				t.Errorf("want NOT READY naming %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestQueueAcceptsExplicitMain(t *testing.T) {
	out, _, code := run(t, `[{"number": 3, "title": "T", `+ready+`, "labels": [{"name": "base:main"}]}]`)
	if code != 0 || !strings.Contains(out, "base: main") {
		t.Fatalf("exit=%d\n%s", code, out)
	}
}

// Triage ranks the queue: a p0 is worked before an older p3, and an issue with
// no priority label sits where triage's default would put it, at p2.
func TestQueueWorksHighestPriorityFirst(t *testing.T) {
	out, _, code := run(t, `[
	  {"number": 3, "title": "Old minor", `+ready+`, "labels": [{"name": "priority:p3"}]},
	  {"number": 8, "title": "Unranked", `+ready+`},
	  {"number": 9, "title": "Urgent", `+ready+`, "labels": [{"name": "priority:p0"}]}
	]`)
	if code != 0 || !strings.Contains(out, "next: #9") {
		t.Fatalf("exit=%d, want #9 next\n%s", code, out)
	}
	if !(strings.Index(out, "#9 ") < strings.Index(out, "#8 ") && strings.Index(out, "#8 ") < strings.Index(out, "#3 ")) {
		t.Errorf("queue not ordered p0, unranked, p3:\n%s", out)
	}
}

// hold is a human asking an approved issue to wait: it is listed, never worked,
// and a queue holding nothing else is not a triage failure.
func TestQueueSkipsHeldIssues(t *testing.T) {
	out, _, code := run(t, `[
	  {"number": 2, "title": "Wait", `+ready+`, "labels": [{"name": "hold"}, {"name": "priority:p0"}]},
	  {"number": 5, "title": "Go", `+ready+`}
	]`)
	if code != 0 || !strings.Contains(out, "next: #5") || !strings.Contains(out, "#2     HOLD") {
		t.Fatalf("exit=%d\n%s", code, out)
	}
	out, _, code = run(t, `[{"number": 2, "title": "Wait", `+ready+`, "labels": [{"name": "hold"}]}]`)
	if code != 0 || !strings.Contains(out, "on hold") {
		t.Fatalf("all held: exit=%d\n%s", code, out)
	}
}
