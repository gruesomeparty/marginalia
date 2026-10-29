package pipeline_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// loopVars runs scripts/loop-vars.sh against a stand-in gh that answers
// `variable list` with vars (JSON), `secret list` with secrets (one name per
// line), and records every call. It returns the output, the exit code and
// the recorded calls.
func loopVars(t *testing.T, vars, secrets string, args ...string) (string, int, []string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	gh := `#!/usr/bin/env bash
echo "$*" >>"$CALLS"
case "$1 $2" in
  "variable list")
    if [ "${*: -2:1}" = --jq ]; then jq -r "${*: -1}" <<<"$VARS"; else echo "$VARS"; fi ;;
  "secret list") printf '%s\n' "$SECRETS" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{filepath.Join(filepath.Dir(script(t)), "loop-vars.sh")}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "CALLS="+calls, "VARS="+vars, "SECRETS="+secrets)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run: %v (%s)", err, out)
		}
		code = exit.ExitCode()
	}
	raw, _ := os.ReadFile(calls)
	return string(out), code, strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// The table is the documentation of every switch; a workflow reading a
// variable it does not describe is how the list goes stale.
func TestLoopVarsDescribesEveryVariableTheWorkflowsRead(t *testing.T) {
	if out, code, _ := loopVars(t, "[]", "", "check"); code != 0 {
		t.Fatalf("check failed (%d):\n%s", code, out)
	}
}

func TestLoopVarsStatus(t *testing.T) {
	vars := `[{"name":"MERGE_LOOP_AUTO","value":"true"},{"name":"TRIAGE_AUTO_APPROVE","value":"false"},
		{"name":"MERGE_TRUSTED","value":"alice bob"},{"name":"OLD_SWITCH","value":"true"}]`
	out, code, _ := loopVars(t, vars, "CLAUDE_CODE_OAUTH_TOKEN")
	if code != 0 {
		t.Fatalf("status failed (%d):\n%s", code, out)
	}
	for _, want := range []string{
		"MERGE_LOOP_AUTO      ON",
		"TRIAGE_AUTO_APPROVE  off (false)",
		"IMPLEMENT_LOOP       off (unset)",
		"MERGE_TRUSTED        alice bob",
		"TRIAGE_TRUSTED       (none)",
		"Set but read by no workflow: OLD_SWITCH",
		"CLAUDE_CODE_OAUTH_TOKEN present",
		"MARGINALIA_URL       missing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

func TestLoopVarsChanges(t *testing.T) {
	on := `[{"name":"IMPLEMENT_LOOP","value":"true"}]`
	for _, c := range []struct {
		vars string
		args []string
		want string
	}{
		{"[]", []string{"on", "merge_loop_auto"}, "variable set MERGE_LOOP_AUTO --repo gruesomeparty/marginalia --body true"},
		{"[]", []string{"off", "IMPLEMENT_LOOP"}, "variable set IMPLEMENT_LOOP --repo gruesomeparty/marginalia --body false"},
		{on, []string{"toggle", "IMPLEMENT_LOOP"}, "variable set IMPLEMENT_LOOP --repo gruesomeparty/marginalia --body false"},
		{"[]", []string{"toggle", "IMPLEMENT_LOOP"}, "variable set IMPLEMENT_LOOP --repo gruesomeparty/marginalia --body true"},
		{"[]", []string{"set", "TRIAGE_TRUSTED", "  alice   bob "}, "variable set TRIAGE_TRUSTED --repo gruesomeparty/marginalia --body alice bob"},
		{"[]", []string{"unset", "MERGE_TRUSTED"}, "variable delete MERGE_TRUSTED --repo gruesomeparty/marginalia"},
	} {
		out, code, calls := loopVars(t, c.vars, "", c.args...)
		if code != 0 || calls[len(calls)-1] != c.want {
			t.Errorf("%v: code %d, calls %q, want last %q\n%s", c.args, code, calls, c.want, out)
		}
	}
}

// A typo must not create a variable no workflow reads, and a switch and a
// login list must not be confused.
func TestLoopVarsRefuses(t *testing.T) {
	for _, args := range [][]string{
		{"on", "MERGE_LOOP"},
		{"set", "NEW_THING", "x"},
		{"on", "MERGE_TRUSTED"},
		{"set", "MERGE_LOOP_AUTO", "true"},
		{"on", ".*"},
		{"on", "MERGE_LOOP_AUTO|"},
		{"set", "TRIAGE_TRUSTED", "o'brien"},
		{"set", "TRIAGE_TRUSTED", "alice,bob"},
		{"toggle"},
		{"frobnicate"},
	} {
		out, code, calls := loopVars(t, "[]", "", args...)
		if code == 0 {
			t.Errorf("%v: accepted\n%s", args, out)
		}
		for _, call := range calls {
			if strings.HasPrefix(call, "variable set") || strings.HasPrefix(call, "variable delete") {
				t.Errorf("%v: wrote anyway: %q", args, call)
			}
		}
	}
}
