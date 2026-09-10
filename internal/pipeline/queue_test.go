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
