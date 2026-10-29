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

type edits struct {
	Number  int      `json:"number"`
	Add     []string `json:"add"`
	Remove  []string `json:"remove"`
	Body    string   `json:"body"`
	Comment string   `json:"comment"`
}

// triage runs scripts/apply-triage.sh, which decides what a triage verdict is
// allowed to change on an issue. The agent that wrote the verdict read text
// anyone could have written, so this script is the boundary, not the prompt.
func triage(t *testing.T, in map[string]any) (edits, string, int) {
	t.Helper()
	path := filepath.Join(filepath.Dir(script(t)), "apply-triage.sh")
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
	var e edits
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &e); err != nil {
			t.Fatalf("output is not JSON: %v\n%s", err, out.String())
		}
	}
	return e, errOut.String(), code
}

func verdict() map[string]any {
	return map[string]any{
		"issue": 40, "type": "bug", "priority": "p1", "size": "s", "ready": true,
		"summary": "Composer loses its draft on reload.",
		"agent_notes": map[string]any{
			"expected":   "A draft survives a reload.",
			"acceptance": []string{"reloading with an open composer restores its text"},
			"paths":      []string{"internal/web/review.html.tmpl"},
			"plan":       []string{"persist the draft per block"},
		},
	}
}

func input(issue map[string]any, v map[string]any, auto bool) map[string]any {
	return map[string]any{"issue": issue, "triage": v, "auto_approve": auto, "trusted": []string{"gruesomeparty"}, "now": "2026-10-29"}
}

func owned(body string, names ...string) map[string]any {
	return map[string]any{"number": 40, "body": body, "labels": labels(names...), "author": map[string]string{"login": "gruesomeparty"}}
}

func TestTriageLabelsAndHidesItsNotes(t *testing.T) {
	e, errOut, code := triage(t, input(owned("The draft is gone after a reload.", "priority:p3", "enhancement"), verdict(), false))
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, errOut)
	}
	for _, want := range []string{"bug", "triaged", "priority:p1", "size:s", "agent-ready"} {
		if !slices.Contains(e.Add, want) {
			t.Errorf("add %v lacks %q", e.Add, want)
		}
	}
	if !slices.Equal(e.Remove, []string{"priority:p3"}) {
		t.Errorf("remove = %v, want only the stale priority (a reporter's type label stays)", e.Remove)
	}
	// The human's text comes first and untouched; everything for agents is in
	// one hidden block, which is where the work loop's gate looks.
	if !strings.HasPrefix(e.Body, "The draft is gone after a reload.\n\n<!-- agent:triage v1\n") || !strings.HasSuffix(e.Body, "-->") {
		t.Errorf("body shape wrong:\n%s", e.Body)
	}
	for _, want := range []string{"expected: A draft survives a reload.", "acceptance:\n- reloading", "paths:\n- internal/web/"} {
		if !strings.Contains(e.Body, want) {
			t.Errorf("block lacks %q:\n%s", want, e.Body)
		}
	}
	if e.Comment != "" {
		t.Errorf("nothing to ask, so no comment; got %q", e.Comment)
	}
}

// Re-triage replaces the block rather than stacking a second one.
func TestTriageReplacesItsOwnBlock(t *testing.T) {
	body := "Report.\n\n<!-- agent:triage v1\nexpected: old\n-->"
	e, _, code := triage(t, input(owned(body), verdict(), false))
	if code != 0 {
		t.Fatal(code)
	}
	if strings.Count(e.Body, "<!-- agent:triage") != 1 || strings.Contains(e.Body, "expected: old") {
		t.Errorf("block not replaced:\n%s", e.Body)
	}
}

// Issue text is anyone's: a verdict quoting "-->" must not close the comment
// early and spill the rest of the notes into the visible issue.
func TestTriageCannotEscapeTheHiddenBlock(t *testing.T) {
	v := verdict()
	v["agent_notes"].(map[string]any)["plan"] = []string{"--> visible <!-- hidden"}
	e, _, code := triage(t, input(owned("x"), v, false))
	if code != 0 {
		t.Fatal(code)
	}
	if strings.Count(e.Body, "-->") != 1 || !strings.HasSuffix(e.Body, "-->") {
		t.Errorf("notes escaped the block:\n%s", e.Body)
	}
}

// approved-for-agent is the human's gate. The script may add it only when the
// repository opted in, and only for a ready, trusted, not-large, not-held issue.
func TestTriageAutoApprovalIsNarrow(t *testing.T) {
	approves := func(issue map[string]any, v map[string]any, auto bool) bool {
		e, errOut, code := triage(t, input(issue, v, auto))
		if code != 0 {
			t.Fatalf("exit=%d: %s", code, errOut)
		}
		return slices.Contains(e.Add, "approved-for-agent")
	}
	if !approves(owned("x"), verdict(), true) {
		t.Error("a ready, small issue by a trusted author should be approved when opted in")
	}
	if approves(owned("x"), verdict(), false) {
		t.Error("approved without the repository opting in")
	}
	stranger := owned("x")
	stranger["author"] = map[string]string{"login": "someone-else"}
	if approves(stranger, verdict(), true) {
		t.Error("approved an untrusted author's issue")
	}
	if approves(owned("x", "hold"), verdict(), true) {
		t.Error("approved an issue on hold")
	}
	large := verdict()
	large["size"] = "l"
	if approves(owned("x"), large, true) {
		t.Error("approved a large issue")
	}
	vague := verdict()
	vague["agent_notes"].(map[string]any)["acceptance"] = []string{}
	if approves(owned("x"), vague, true) {
		t.Error("approved an issue with no acceptance criterion")
	}
}

// The script never touches labels outside its set: a base:* label decides
// where work auto-merges, and approved-for-agent, once a human set it, stays.
func TestTriageLeavesHumanLabelsAlone(t *testing.T) {
	v := verdict()
	v["ready"] = false
	v["needs"] = "info"
	v["question"] = "Which browser?"
	e, _, code := triage(t, input(owned("x", "approved-for-agent", "base:feature/x", "hold", "agent-ready"), v, true))
	if code != 0 {
		t.Fatal(code)
	}
	for _, l := range append(append([]string{}, e.Add...), e.Remove...) {
		if l == "approved-for-agent" || l == "hold" || strings.HasPrefix(l, "base:") {
			t.Errorf("touched %q", l)
		}
	}
	if !slices.Contains(e.Remove, "agent-ready") || !slices.Contains(e.Add, "needs-info") {
		t.Errorf("add=%v remove=%v", e.Add, e.Remove)
	}
	if e.Comment != "Which browser?" {
		t.Errorf("a question for a human is the one visible comment; got %q", e.Comment)
	}
}

func TestTriageRefusesAnInvalidVerdict(t *testing.T) {
	for field, val := range map[string]any{"type": "feature; rm -rf", "priority": "urgent", "size": "xl", "needs": "approval", "issue": 41} {
		t.Run(field, func(t *testing.T) {
			v := verdict()
			v[field] = val
			_, errOut, code := triage(t, input(owned("x"), v, false))
			if code != 1 || !strings.Contains(errOut, "invalid verdict") {
				t.Errorf("exit=%d stderr=%q", code, errOut)
			}
		})
	}
}

func TestTriageMarksDuplicates(t *testing.T) {
	v := verdict()
	v["duplicate_of"] = 12
	e, _, code := triage(t, input(owned("x"), v, true))
	if code != 0 {
		t.Fatal(code)
	}
	if !slices.Contains(e.Add, "duplicate") || slices.Contains(e.Add, "agent-ready") || slices.Contains(e.Add, "approved-for-agent") {
		t.Errorf("add = %v", e.Add)
	}
	if !strings.Contains(e.Comment, "#12") {
		t.Errorf("comment = %q", e.Comment)
	}
}
