package pipeline_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	reviewcfg "github.com/gruesomeparty/marginalia/internal/review"
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
		"owner": "gruesomeparty", "trusted": "", "review_sha": gateHead, "domain": "complicated", "approved_sha": "", "auto": "true",
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
		"workflow": {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": ".github/workflows/ci.yml"}} }, ".github/workflows/ci.yml"},
		"script":   {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "scripts/merge-gate.sh"}} }, "scripts/merge-gate.sh"},
		"go.mod":   {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "go.mod"}} }, "go.mod"},
		"identity": {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "internal/hosted/oauth.go"}} }, "oauth.go"},
		"prd.md":   {func(in map[string]any) { prOf(in)["files"] = []map[string]any{{"path": "PRD.md"}} }, "PRD.md"},
		"loop skill": {func(in map[string]any) {
			prOf(in)["files"] = []map[string]any{{"path": "skills/implement-approved/SKILL.md"}}
		}, "skills/implement-approved"},
		"deploy": {func(in map[string]any) {
			prOf(in)["files"] = []map[string]any{{"path": "deploy/cloudflare/src/index.ts"}}
		}, "deploy/"},
		"lines":      {func(in map[string]any) { prOf(in)["additions"] = 1490; prOf(in)["deletions"] = 20 }, "1510 lines of code"},
		"complex":    {func(in map[string]any) { in["domain"] = "complex" }, "calls the change complex"},
		"chaotic":    {func(in map[string]any) { in["domain"] = "chaotic" }, "calls the change chaotic"},
		"no domain":  {func(in map[string]any) { delete(in, "domain") }, "names no domain"},
		"odd domain": {func(in map[string]any) { in["domain"] = "simple" }, "names no domain"},
		"untested go": {func(in map[string]any) {
			prOf(in)["files"] = []map[string]any{{"path": "internal/server/feedback.go"}, {"path": "internal/server/testdata/a.json"}}
		}, "Go code without changing a test"},
		"files": {func(in map[string]any) {
			var fs []map[string]any
			for i := range 41 {
				fs = append(fs, map[string]any{"path": "internal/web/f" + string(rune('a'+i)) + ".go"})
			}
			prOf(in)["files"] = fs
		}, "41 files"},
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

// CLAUDE.md and the review-doc skill document rather than decide, and nearly
// every PR updates them; guarding them would send every PR to the maintainer.
func TestMergeGateLetsDocsThrough(t *testing.T) {
	in := lowRisk()
	prOf(in)["files"] = []map[string]any{{"path": "CLAUDE.md"}, {"path": "skills/review-doc/SKILL.md"}, {"path": "internal/web/review.html.tmpl"}}
	if out := gate(t, in); out.Decision != "merge" {
		t.Fatalf("got %+v, want merge", out)
	}
	// Only that one file: a sibling skill whose name merely starts the same
	// way, or a helper added beside it, is something the loops may run.
	for _, p := range []string{"skills/review-doc-x/SKILL.md", "skills/review-doc/helper.sh"} {
		prOf(in)["files"] = []map[string]any{{"path": p}}
		if out := gate(t, in); out.Decision != "human" {
			t.Fatalf("%s: got %+v, want human", p, out)
		}
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
	cfg, err := reviewcfg.Load(path)
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

// A file moved out of a sensitive directory still touched it: the gate reads
// the old path the REST listing gives, not only the new one.
// Tests, testdata and docs do not count toward the size ceiling: a
// well-tested change must not read as a riskier one. Without per-file counts
// every line counts.
func TestMergeGateCountsCodeNotTests(t *testing.T) {
	in := lowRisk()
	prOf(in)["additions"], prOf(in)["deletions"] = 2900, 100
	prOf(in)["files"] = []map[string]any{
		{"path": "internal/sqlstmt/scan.go", "additions": 1400, "deletions": 50},
		{"path": "internal/sqlstmt/scan_test.go", "additions": 900, "deletions": 0},
		{"path": "internal/sqlstmt/testdata/big.sql", "additions": 400, "deletions": 0},
		{"path": "internal/browsertest/sql_test.go", "additions": 100, "deletions": 0},
		{"path": "docs/sql.md", "additions": 100, "deletions": 50},
	}
	if out := gate(t, in); out.Decision != "merge" {
		t.Fatalf("1450 lines of code and the rest tests: got %+v, want merge", out)
	}
	prOf(in)["files"].([]map[string]any)[0]["additions"] = 1500
	if out := gate(t, in); out.Decision != "human" || !strings.Contains(strings.Join(out.Reasons, " "), "1550 lines of code") {
		t.Fatalf("1550 lines of code: got %+v, want human", out)
	}
	delete(prOf(in)["files"].([]map[string]any)[1], "additions")
	if out := gate(t, in); out.Decision != "human" || !strings.Contains(strings.Join(out.Reasons, " "), "3000 lines of code") {
		t.Fatalf("a file without counts: got %+v, want every line counted", out)
	}
}

// A clear or complicated call removes no other reason to ask.
func TestMergeGateDomainNeverLowersAFloor(t *testing.T) {
	for _, d := range []string{"clear", "complicated"} {
		in := lowRisk()
		in["domain"] = d
		prOf(in)["files"] = []map[string]any{{"path": "scripts/merge-gate.sh"}}
		if out := gate(t, in); out.Decision != "human" {
			t.Fatalf("%s on a sensitive path: got %+v, want human", d, out)
		}
	}
}

func TestMergeGateCountsTheOldPathOfARename(t *testing.T) {
	in := lowRisk()
	prOf(in)["files"] = []map[string]any{{"path": "docs/ci.yml", "previous_filename": ".github/workflows/ci.yml"}}
	out := gate(t, in)
	if out.Decision != "human" || !strings.Contains(strings.Join(out.Reasons, " "), ".github/workflows/ci.yml") {
		t.Fatalf("got %+v, want human naming the old path", out)
	}
}

// Times compare as instants: fractional seconds or a +00:00 offset must not
// sort a decision before the revision it was made on.
func TestMergeVerdictComparesTimesAsInstants(t *testing.T) {
	got := mergeVerdict(t,
		mergeEvent("merge_pr", "2026-10-29T08:00:00.250Z", ""),
		mergeEvent("review_done", "2026-10-29T08:00:01+00:00", ""))
	if got.Decision != "merge" {
		t.Fatalf("got %q, want merge", got.Decision)
	}
	stale := mergeVerdict(t,
		mergeEvent("merge_pr", "2026-10-29T07:59:59.900Z", ""),
		mergeEvent("review_done", "not a time", ""))
	if stale.Decision != "pending" {
		t.Fatalf("got %q, want pending for events before or without a time", stale.Decision)
	}
}

func TestMergeVerdictRefusesAnUnparseablePublishTime(t *testing.T) {
	var out mergeVerdictOut
	if code := runJSON(t, "merge-verdict.sh", map[string]any{"events": []any{}, "published_at": "yesterday", "number": 1}, &out); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// fakeGH is a stand-in gh for merge-loop.sh: it serves pr-<n>.json and
// files-<n>.json from dir, fails `pr diff` for any PR listed in nodiff and
// the file listing for any PR listed in nofiles, and logs every call.
const fakeGH = `#!/usr/bin/env bash
dir="$(dirname "$0")"
echo "gh $*" >>"$dir/calls.log"
case "$1 $2" in
  "pr view") cat "$dir/pr-$3.json" ;;
  "pr diff") if grep -qx "$3" "$dir/nodiff" 2>/dev/null; then echo "diff too large" >&2; exit 1; fi; echo "diff --git a/x b/x" ;;
  "api --paginate")
    case "$3" in
      */files) n="${3%/files}"; n="${n##*/}"; if grep -qx "$n" "$dir/nofiles" 2>/dev/null; then exit 1; fi; cat "$dir/files-$n.json" ;;
      *) echo '[]' ;;
    esac ;;
esac
exit 0
`

// One PR whose diff GitHub refuses to serve must not end the sweep: it falls
// back to asking for a GitHub approval, and the next PR is still decided. A PR
// whose file listing fails is skipped, never decided on the weaker list that
// cannot see a rename's old path.
func TestMergeLoopSurvivesARefusedDiff(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(fakeGH), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nodiff"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nofiles"), []byte("3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3} {
		p := prOf(lowRisk())
		p["number"], p["state"], p["latestReviews"] = n, "OPEN", []any{}
		if n == 1 {
			p["labels"] = labels("review:concerns")
		}
		write(fmt.Sprintf("pr-%d.json", n), p)
		write(fmt.Sprintf("files-%d.json", n), []map[string]any{{"filename": "internal/web/review.html.tmpl"}})
	}
	// Both PRs carry a review of their head, in this workflow's own comment.
	review := `[{"id": 9, "user": {"login": "github-actions[bot]"}, "body": "<!-- agent:review v1\nsha: ` + gateHead + `\ndomain: complicated\nnotes: x\ndomain: complex\n-->"}]`
	gh := strings.Replace(fakeGH, `*) echo '[]' ;;`, `*) echo '`+review+`' ;;`, 1)
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", filepath.Join(filepath.Dir(script(t)), "merge-loop.sh"), "1", "2", "3")
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"REPO=o/r", "OWNER=gruesomeparty", "GH_TOKEN=x", "MERGE_LOOP_AUTO=true",
		"MARGINALIA_URL=http://127.0.0.1:1", "MARGINALIA_MCP_TOKEN=t", "MERGE_TOKEN=", "DRY_RUN=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("merge-loop failed: %v\n%s", err, out)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(calls)
	if !strings.Contains(string(out), "would not serve the diff") {
		t.Errorf("no warning for the refused diff:\n%s", out)
	}
	if !strings.Contains(log, "issues/1/labels -f labels[]=merge:human") {
		t.Errorf("#1 was not handed to the maintainer:\n%s", log)
	}
	if !strings.Contains(log, "pr merge 2 --repo o/r --squash --delete-branch --match-head-commit "+gateHead) {
		t.Errorf("#2 was not merged after #1's diff failed:\n%s", log)
	}
	if strings.Contains(log, "pr merge 1 ") {
		t.Errorf("#1 was merged:\n%s", log)
	}
	if strings.Contains(log, "pr merge 3 ") || !strings.Contains(string(out), "#3: could not list the PR's files") {
		t.Errorf("#3 was decided without its file listing:\n%s\n%s", out, log)
	}
}

// A conflict with main is flagged, never resolved: the PR gets merge:conflict
// and a comment naming its head, and loses both once it merges cleanly. A PR
// whose mergeability is not known yet is asked again, and one the gate skips
// (Renovate rebases its own, or one put on hold) is left alone.
func TestMergeLoopFlagsConflicts(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gh := strings.Replace(fakeGH, `"pr view") cat`,
		`"pr view") case "$*" in *--jq*) cat "$dir/mergeable-$3"; exit 0 ;; esac; cat`, 1)
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mergeable-4"), []byte("CONFLICTING\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3, 4, 5} {
		p := prOf(lowRisk())
		p["number"], p["state"], p["latestReviews"] = n, "OPEN", []any{}
		switch n {
		case 5:
			p["mergeable"], p["labels"] = "CONFLICTING", labels("review:pass", "merge:conflict", "hold")
		case 1:
			p["mergeable"] = "CONFLICTING"
		case 2:
			p["labels"] = labels("review:pass", "merge:conflict")
		case 3:
			p["mergeable"], p["headRefName"] = "CONFLICTING", "renovate/go-deps"
		case 4:
			p["mergeable"] = "UNKNOWN"
		}
		write(fmt.Sprintf("pr-%d.json", n), p)
		write(fmt.Sprintf("files-%d.json", n), []map[string]any{{"filename": "internal/web/review.html.tmpl"}})
	}

	cmd := exec.Command("bash", filepath.Join(filepath.Dir(script(t)), "merge-loop.sh"), "1", "2", "3", "4", "5")
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"REPO=o/r", "OWNER=gruesomeparty", "GH_TOKEN=x", "MERGE_LOOP_AUTO=",
		"MARGINALIA_URL=", "MARGINALIA_MCP_TOKEN=", "MERGE_TOKEN=", "DRY_RUN=", "MERGEABLE_RETRY=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("merge-loop failed: %v\n%s", err, out)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(calls)
	for _, n := range []string{"1", "4"} {
		if !strings.Contains(log, "issues/"+n+"/labels -f labels[]=merge:conflict") ||
			!strings.Contains(log, "issues/"+n+"/comments -F body=@") {
			t.Errorf("#%s was not flagged as conflicting:\n%s", n, log)
		}
	}
	if !strings.Contains(log, "--method DELETE repos/o/r/issues/2/labels/merge%3Aconflict") {
		t.Errorf("#2's resolved conflict kept its label:\n%s", log)
	}
	if strings.Contains(log, "issues/3/labels") || strings.Contains(log, "issues/3/comments -F") {
		t.Errorf("Renovate's PR was flagged:\n%s", log)
	}
	if strings.Contains(log, "issues/5/labels") || strings.Contains(log, "issues/5/comments -F") {
		t.Errorf("a PR put on hold while conflicting was touched:\n%s", log)
	}
	if strings.Contains(log, "pr merge") {
		t.Errorf("a conflicting PR was merged:\n%s", log)
	}
}
