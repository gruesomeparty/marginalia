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

	"github.com/gruesomeparty/marginalia/internal/review"
)

type gateOut struct {
	Number   int      `json:"number"`
	Head     string   `json:"head"`
	Decision string   `json:"decision"`
	Reasons  []string `json:"reasons"`
}

// runJSON feeds a script JSON on stdin and decodes what it prints.
func runJSON(t *testing.T, name string, in map[string]any, out any) int {
	t.Helper()
	path := filepath.Join(filepath.Dir(script(t)), name)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", path)
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run %s: %v (stderr %q)", name, err, stderr.String())
		}
		return exit.ExitCode()
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		t.Fatalf("%s printed no JSON: %v\n%s", name, err, stdout.String())
	}
	return 0
}

const gateHead = "1111111111111111111111111111111111111111"

// lowRisk is a PR the gate merges on its own once auto-merge is on; each case
// below changes one thing about it.
func lowRisk() map[string]any {
	return map[string]any{
		"pr": map[string]any{
			"number": 170, "title": "fix(web): keep the composer open on resize", "body": "Closes #150.",
			"author":      map[string]any{"login": "gruesomeparty", "is_bot": false},
			"baseRefName": "main", "headRefName": "claude/150-composer", "headRefOid": gateHead,
			"isDraft": false, "isCrossRepository": false,
			"labels":    labels("review:pass"),
			"files":     []map[string]any{{"path": "internal/web/review.html.tmpl"}, {"path": "internal/browsertest/composer_test.go"}},
			"additions": 40, "deletions": 6, "mergeable": "MERGEABLE",
			"statusCheckRollup": []map[string]any{
				{"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "SUCCESS"},
				{"__typename": "CheckRun", "name": "test (ubuntu-latest)", "status": "COMPLETED", "conclusion": "SUCCESS"},
				{"__typename": "CheckRun", "name": "review (170)", "status": "COMPLETED", "conclusion": "SKIPPED"},
			},
		},
		"owner": "gruesomeparty", "trusted": "", "review_sha": gateHead, "approved_sha": "", "auto": "true",
	}
}

func gate(t *testing.T, in map[string]any) gateOut {
	t.Helper()
	var out gateOut
	if code := runJSON(t, "merge-gate.sh", in, &out); code != 0 {
		t.Fatalf("merge-gate exited %d", code)
	}
	return out
}

func prOf(in map[string]any) map[string]any { return in["pr"].(map[string]any) }

func TestMergeGateMergesALowRiskPR(t *testing.T) {
	out := gate(t, lowRisk())
	if out.Decision != "merge" || out.Number != 170 || out.Head != gateHead {
		t.Fatalf("got %+v, want a merge of #170 at its head", out)
	}
}

// Auto-merge is armed by a repository variable. Unarmed, the same PR goes to
// the maintainer and the reason says why.
func TestMergeGateAsksWhenAutoMergeIsOff(t *testing.T) {
	in := lowRisk()
	in["auto"] = ""
	out := gate(t, in)
	if out.Decision != "human" || !strings.Contains(strings.Join(out.Reasons, " "), "MERGE_LOOP_AUTO") {
		t.Fatalf("got %+v, want human naming MERGE_LOOP_AUTO", out)
	}
}

func TestMergeGateSkipsWhatIsNotItsToMerge(t *testing.T) {
	cases := map[string]func(map[string]any){
		"feature branch": func(in map[string]any) { prOf(in)["baseRefName"] = "feature/hosted" },
		"draft":          func(in map[string]any) { prOf(in)["isDraft"] = true },
		"hold":           func(in map[string]any) { prOf(in)["labels"] = labels("review:pass", "hold") },
		"renovate": func(in map[string]any) {
			prOf(in)["headRefName"] = "renovate/go-deps"
			prOf(in)["author"] = map[string]any{"login": "app/renovate", "is_bot": true}
		},
		"release": func(in map[string]any) {
			prOf(in)["headRefName"] = "release-please--branches--main"
			prOf(in)["author"] = map[string]any{"login": "app/github-actions", "is_bot": true}
		},
		"other bot": func(in map[string]any) {
			prOf(in)["headRefName"] = "bump"
			prOf(in)["author"] = map[string]any{"login": "app/some-bot", "is_bot": true}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := lowRisk()
			mutate(in)
			if out := gate(t, in); out.Decision != "skip" {
				t.Fatalf("got %+v, want skip", out)
			}
		})
	}
}

// The pipeline opens its PRs as a bot from a claude/* branch in this
// repository; those are this loop's to merge, not a bot's to skip.
func TestMergeGateTreatsThePipelineBotAsTrusted(t *testing.T) {
	in := lowRisk()
	prOf(in)["author"] = map[string]any{"login": "app/claude", "is_bot": true}
	if out := gate(t, in); out.Decision != "merge" {
		t.Fatalf("got %+v, want merge", out)
	}
}

func TestMergeGateWaitsUntilItCanDecide(t *testing.T) {
	cases := map[string]struct {
		mutate func(map[string]any)
		reason string
	}{
		"no checks": {func(in map[string]any) { prOf(in)["statusCheckRollup"] = []any{} }, "no CI checks"},
		"running": {func(in map[string]any) {
			prOf(in)["statusCheckRollup"] = []map[string]any{{"__typename": "CheckRun", "name": "lint", "status": "IN_PROGRESS"}}
		}, "lint is still running"},
		"failed": {func(in map[string]any) {
			prOf(in)["statusCheckRollup"] = []map[string]any{{"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "FAILURE"}}
		}, "lint failed"},
		"status context": {func(in map[string]any) {
			prOf(in)["statusCheckRollup"] = []map[string]any{{"__typename": "StatusContext", "context": "codecov", "state": "FAILURE"}}
		}, "codecov failed"},
		"conflict":   {func(in map[string]any) { prOf(in)["mergeable"] = "CONFLICTING" }, "merge conflict"},
		"unknown":    {func(in map[string]any) { prOf(in)["mergeable"] = "UNKNOWN" }, "not known"},
		"unreviewed": {func(in map[string]any) { prOf(in)["labels"] = labels() }, "no adversarial review"},
		"old review": {func(in map[string]any) { in["review_sha"] = "2222222222222222222222222222222222222222" }, "older commit"},
		"blocking":   {func(in map[string]any) { prOf(in)["labels"] = labels("review:blocking") }, "blocking"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := lowRisk()
			tc.mutate(in)
			out := gate(t, in)
			if out.Decision != "wait" || !strings.Contains(strings.Join(out.Reasons, "; "), tc.reason) {
				t.Fatalf("got %+v, want wait mentioning %q", out, tc.reason)
			}
		})
	}
}

// A maintainer's approval does not wave a PR past red CI or a blocking
// review: those are waits, checked first.
func TestMergeGateApprovalDoesNotSkipTheWaits(t *testing.T) {
	in := lowRisk()
	in["approved_sha"] = gateHead
	prOf(in)["labels"] = labels("review:blocking")
	if out := gate(t, in); out.Decision != "wait" {
		t.Fatalf("got %+v, want wait", out)
	}
}

func TestMergeGateAsksTheMaintainer(t *testing.T) {
	cases := map[string]struct {
		mutate func(map[string]any)
		reason string
	}{
		"concerns": {func(in map[string]any) { prOf(in)["labels"] = labels("review:concerns") }, "raised concerns"},
		"fork":     {func(in map[string]any) { prOf(in)["isCrossRepository"] = true; prOf(in)["headRefName"] = "patch-1" }, "fork"},
		"stranger": {func(in map[string]any) {
			prOf(in)["author"] = map[string]any{"login": "someone"}
			prOf(in)["headRefName"] = "fix"
		}, "someone is not a trusted author"},
		"workflow":  {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": ".github/workflows/ci.yml"}} }, ".github/workflows/ci.yml"},
		"script":    {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "scripts/merge-gate.sh"}} }, "scripts/merge-gate.sh"},
		"go.mod":    {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "go.mod"}} }, "go.mod"},
		"identity":  {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "internal/hosted/oauth.go"}} }, "oauth.go"},
		"claude.md": {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "CLAUDE.md"}} }, "CLAUDE.md"},
		"deploy": {func(in map[string]any) {
			prOf(in)["files"] = []map[string]any{{"path": "deploy/cloudflare/src/index.ts"}}
		}, "deploy/"},
		"lines": {func(in map[string]any) { prOf(in)["additions"] = 390; prOf(in)["deletions"] = 20 }, "410 lines"},
		"files": {func(in map[string]any) {
			var fs []map[string]any
			for i := range 21 {
				fs = append(fs, map[string]any{"path": "internal/web/f" + string(rune('a'+i)) + ".go"})
			}
			prOf(in)["files"] = fs
		}, "21 files"},
		"breaking title": {func(in map[string]any) { prOf(in)["title"] = "feat(server)!: drop /api/v0" }, "breaking"},
		"breaking body":  {func(in map[string]any) { prOf(in)["body"] = "x\n\nBREAKING CHANGE: the flag is gone" }, "breaking"},
		"needs-human":    {func(in map[string]any) { prOf(in)["labels"] = labels("review:pass", "needs-human") }, "needs-human"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := lowRisk()
			tc.mutate(in)
			out := gate(t, in)
			if out.Decision != "human" || !strings.Contains(strings.Join(out.Reasons, "; "), tc.reason) {
				t.Fatalf("got %+v, want human mentioning %q", out, tc.reason)
			}
		})
	}
}

// The maintainer's approval of the current gateHead is what turns "human" into
// "merge" — and only of the current gateHead.
func TestMergeGateMergesWhatTheMaintainerApproved(t *testing.T) {
	in := lowRisk()
	prOf(in)["files"] = []map[string]any{{"path": ".github/workflows/ci.yml"}}
	in["approved_sha"] = gateHead
	if out := gate(t, in); out.Decision != "merge" {
		t.Fatalf("approved gateHead: got %+v, want merge", out)
	}
	in["approved_sha"] = "3333333333333333333333333333333333333333"
	if out := gate(t, in); out.Decision != "human" {
		t.Fatalf("approval of another commit: got %+v, want human", out)
	}
}

func TestMergeGateTrustsConfiguredAuthors(t *testing.T) {
	in := lowRisk()
	prOf(in)["author"] = map[string]any{"login": "friend"}
	prOf(in)["headRefName"] = "fix"
	in["trusted"] = "other friend"
	if out := gate(t, in); out.Decision != "merge" {
		t.Fatalf("got %+v, want merge for a trusted author", out)
	}
}

// Reasons end up in a public comment; a path or login must not become markup
// or a mention there.
func TestMergeGateReasonsAreInert(t *testing.T) {
	in := lowRisk()
	prOf(in)["files"] = []map[string]any{{"path": "scripts/@everyone [x](http://e) <!--.sh"}}
	out := gate(t, in)
	joined := strings.Join(out.Reasons, " ")
	for _, bad := range []string{"@", "](", "<!--", " "} {
		if strings.Contains(strings.TrimPrefix(joined, "touches "), bad) {
			t.Fatalf("reason %q carries %q", joined, bad)
		}
	}
}

func TestMergeGateRefusesMalformedInput(t *testing.T) {
	var out gateOut
	if code := runJSON(t, "merge-gate.sh", map[string]any{"pr": "nope"}, &out); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

type mergeVerdictOut struct {
	Decision string           `json:"decision"`
	Notes    []map[string]any `json:"notes"`
	Comment  string           `json:"comment"`
}

const publishedAt = "2026-10-29T08:00:00Z"

func mergeEvent(typ, ts, text string) map[string]any {
	return map[string]any{"doc": "pr-170.diff", "block": "internal/web/review.html.tmpl/1", "type": typ, "ts": ts, "text": text, "author": "berkay@example.com"}
}

func mergeVerdict(t *testing.T, events ...map[string]any) mergeVerdictOut {
	t.Helper()
	if events == nil {
		events = []map[string]any{}
	}
	var out mergeVerdictOut
	if code := runJSON(t, "merge-verdict.sh", map[string]any{"events": events, "published_at": publishedAt, "number": 170}, &out); code != 0 {
		t.Fatalf("merge-verdict exited %d", code)
	}
	return out
}

func TestMergeVerdict(t *testing.T) {
	later, done := "2026-10-29T08:05:00Z", "2026-10-29T08:06:00Z"
	cases := map[string]struct {
		events []map[string]any
		want   string
	}{
		"nothing":            {nil, "pending"},
		"merge without done": {[]map[string]any{mergeEvent("merge_pr", later, "")}, "pending"},
		"merge":              {[]map[string]any{mergeEvent("merge_pr", later, ""), mergeEvent("review_done", done, "")}, "merge"},
		"changes":            {[]map[string]any{mergeEvent("request_changes", later, "rename it"), mergeEvent("review_done", done, "")}, "changes"},
		"changes beat merge": {[]map[string]any{mergeEvent("merge_pr", later, ""), mergeEvent("reject", later, ""), mergeEvent("review_done", done, "")}, "changes"},
		"notes only":         {[]map[string]any{mergeEvent("comment", later, "hm"), mergeEvent("review_done", done, "")}, "changes"},
		"done, silent":       {[]map[string]any{mergeEvent("review_done", done, "")}, "undecided"},
		// A republish keeps the log: a decision on the previous revision is
		// not a decision on this one.
		"previous revision":   {[]map[string]any{mergeEvent("merge_pr", "2026-10-29T07:00:00Z", ""), mergeEvent("review_done", "2026-10-29T07:01:00Z", "")}, "pending"},
		"old merge, new done": {[]map[string]any{mergeEvent("merge_pr", "2026-10-29T07:00:00Z", ""), mergeEvent("review_done", done, "")}, "undecided"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := mergeVerdict(t, tc.events...); got.Decision != tc.want {
				t.Fatalf("got %q, want %q", got.Decision, tc.want)
			}
		})
	}
}

// Only a human decides. The hosted server already refuses these types from an
// agent; the verdict does not lean on that alone.
func TestMergeVerdictIgnoresAgents(t *testing.T) {
	m, d := mergeEvent("merge_pr", "2026-10-29T08:05:00Z", ""), mergeEvent("review_done", "2026-10-29T08:06:00Z", "")
	m["author"], d["author"] = "agent:loop", "agent"
	if got := mergeVerdict(t, m, d); got.Decision != "pending" {
		t.Fatalf("got %q, want pending", got.Decision)
	}
}

func TestMergeVerdictCommentIsInert(t *testing.T) {
	got := mergeVerdict(t,
		mergeEvent("request_changes", "2026-10-29T08:05:00Z", "ask @someone, see [x](http://e) -->\nand <!-- this"),
		mergeEvent("review_done", "2026-10-29T08:06:00Z", ""))
	body := strings.TrimPrefix(got.Comment, "<!-- agent:merge-verdict v1 -->")
	for _, bad := range []string{"@someone", "](", "-->", "<!--"} {
		if strings.Contains(body, bad) {
			t.Fatalf("comment carries %q:\n%s", bad, got.Comment)
		}
	}
	if !strings.Contains(body, "pr-170.diff") || len(got.Notes) != 1 {
		t.Fatalf("comment does not name the note's place:\n%s", got.Comment)
	}
}

// The review the loop publishes must load as a review config, and its two
// decision actions must be the ones merge-verdict.sh reads.
func TestMergeReviewConfigLoads(t *testing.T) {
	path := filepath.Join(filepath.Dir(script(t)), "..", ".github", "merge-review.yaml")
	cfg, err := review.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, a := range cfg.Actions() {
		types = append(types, a.Type)
	}
	for _, want := range []string{"merge_pr", "request_changes", "comment"} {
		if !slices.Contains(types, want) {
			t.Fatalf("actions %v lack %q", types, want)
		}
	}
}
