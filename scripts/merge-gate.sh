#!/usr/bin/env bash
# Decides what the merge loop does with one pull request into main: merge it,
# hand it to the maintainer, wait, or leave it alone.
#
# Usage (what .github/workflows/merge-prs.yml does):
#   jq -n --argjson pr "$(gh pr view N --json number,title,body,author,baseRefName,headRefName,headRefOid,isDraft,isCrossRepository,labels,files,additions,deletions,mergeable,statusCheckRollup)" \
#         --arg owner gruesomeparty --arg trusted "$MERGE_TRUSTED" \
#         --arg review_sha "$SHA_THE_REVIEW_COMMENT_NAMES" \
#         --arg approved_sha "$SHA_THE_MAINTAINER_APPROVED" \
#         --arg domain "$DOMAIN_THAT_REVIEW_NAMES" \
#         --arg auto "$MERGE_LOOP_AUTO" \
#         '{pr: $pr, owner: $owner, trusted: $trusted, review_sha: $review_sha,
#           domain: $domain, approved_sha: $approved_sha, auto: $auto}' \
#     | scripts/merge-gate.sh
#
# Not every PR needs a human, and this script is what says which ones do. It
# is a script and not a prompt because the repository is public: the PR's
# title, body and diff were written by whoever opened it, and nothing in them
# may be able to merge it. The judgment it leans on is the adversarial review
# (review-prs.yml), and only through the label scripts/apply-review.sh set,
# the commit that review names, and the Cynefin domain it recorded for the
# change. The domain can only add a reason to ask: clear and complicated
# changes have one right answer that the review and CI can check, complex ones
# do not, and the floors below hold whatever the review called it.
#
# Decisions, in the order they are checked:
#
#   skip   not this loop's PR: not into main (feature/* branches have
#          integration-automerge.yml), a draft, on hold, Renovate (it merges
#          itself), release-please (the maintainer merges releases), another bot
#   wait   not decidable yet: CI not green on the head, a merge conflict,
#          mergeability unknown, no adversarial review of the current head, or
#          a review:blocking label (the author fixes it; nobody is asked to
#          wave it through)
#   merge  the maintainer approved this exact head (in Marginalia, or as a
#          GitHub approval) — or nothing below asks for a human and
#          MERGE_LOOP_AUTO is true
#   human  everything else, with every reason listed: review:concerns, an
#          author who is neither the owner, a trusted login nor the pipeline,
#          a fork, a review that calls the change complex or chaotic or names
#          no domain, a path that changes how the project is built, released,
#          deployed or trusted, Go code changed with no test changed, more code
#          than one review should vouch for (tests, testdata and docs are not
#          counted: a well-tested change must not read as a riskier one), a
#          breaking change, the needs-human label, or auto-merge being off
#
# Prints {number, head, decision, reasons} and exits 0; exit 2 on malformed
# input.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "merge-gate: jq is required" >&2
  exit 2
fi

input="$(cat)"
if ! jq -e '(.pr | type == "object") and (.pr.number | type == "number") and (.owner | type == "string")' >/dev/null 2>&1 <<<"$input"; then
  echo "merge-gate: expected {pr, owner, ...} on stdin" >&2
  exit 2
fi

jq '
  # Paths whose change alters how the project is built, released, deployed,
  # reviewed or trusted. A PR touching one always goes to the maintainer, even
  # with a clean review: these are the files that decide what the loops, CI
  # and the release may do, and the boundaries the PRD names as invariants.
  # CLAUDE.md and the SKILL.md of the review-doc skill are not on it: nearly
  # every PR updates them, they document rather than decide, and the review
  # reads them anyway.
  def sensitive:
    test("^(\\.github/|scripts/|skills/(?!review-doc/SKILL\\.md$)|\\.claude/|\\.claude-plugin/|deploy/|internal/pipeline/"
      + "|internal/storage/|internal/identity/|internal/mcpserver/|internal/review/"
      + "|internal/hosted/(identity|oauth|limits)\\.go$"
      + "|(PRD|SECURITY)\\.md$|LICENSE$|Dockerfile$|\\.goreleaser|renovate\\.json$"
      + "|\\.release-please|go\\.(mod|sum)$)");
  # More code than an adversarial pass should vouch for alone. Tests, testdata
  # and docs do not count toward it.
  def max_code_lines: 1500;
  def max_files: 40;
  def not_code: test("(_test\\.go$|/testdata/|^internal/browsertest/|\\.md$|^docs/)");
  def is_test: test("(_test\\.go$|^internal/browsertest/)");
  # Names that end up in a comment: a branch or path may hold anything a
  # filesystem allows, and none of it may become markup or a mention.
  def safe: tostring | gsub("[^A-Za-z0-9._/+-]"; "") | .[0:100];
  def name_of: tostring | gsub("[^A-Za-z0-9._/+() -]"; "") | .[0:100];

  .pr as $pr
  | ([$pr.labels // [] | .[].name]) as $labels
  | ($pr.headRefOid // "") as $head
  | ($pr.headRefName // "") as $branch
  | ($pr.author.login // "") as $author
  | ($pr.author.is_bot // false) as $bot
  | ([(.trusted // "") | splits("[ ,]+") | select(length > 0)] + [.owner]) as $trusted
  | ($pr.isCrossRepository // false) as $fork
  | (($branch | startswith("claude/")) and ($fork | not)) as $pipeline

  | ([$pr.statusCheckRollup // [] | .[]
      | if .__typename == "StatusContext" or has("state") and (has("conclusion") | not)
        then {name: (.context // "status"), ok: (.state == "SUCCESS"), done: (.state != "PENDING" and .state != "EXPECTED")}
        else {name: (.name // "check"), ok: ((.conclusion // "") | IN("SUCCESS", "SKIPPED", "NEUTRAL")),
              done: ((.status // "") == "COMPLETED")} end]) as $checks

  | (if ($pr.baseRefName // "") != "main" then "targets \($pr.baseRefName // "nothing" | safe), not main"
     elif $pr.isDraft then "is a draft"
     elif ($labels | index("hold")) then "is on hold"
     elif ($branch | startswith("renovate/")) then "is Renovate'"'"'s, which merges itself on green CI"
     elif ($branch | startswith("release-please--")) then "is a release, which the maintainer merges"
     elif $bot and ($pipeline | not) then "is a bot'"'"'s (\($author | safe))"
     else null end) as $skip

  | ([ (if ($checks | length) == 0 then "no CI checks on the head yet" else empty end),
       ($checks[] | select(.done | not) | "\(.name | name_of) is still running"),
       ($checks[] | select(.done and (.ok | not)) | "\(.name | name_of) failed"),
       (if $pr.mergeable == "CONFLICTING" then "has a merge conflict with main"
        elif ($pr.mergeable // "UNKNOWN") != "MERGEABLE" then "mergeability is not known yet" else empty end),
       (if ([$labels[] | select(test("^review:(pass|concerns|blocking)$"))] | length) == 0
        then "has no adversarial review yet"
        elif (.review_sha // "") != $head then "the adversarial review is of an older commit"
        else empty end),
       (if ($labels | index("review:blocking")) then "the adversarial review found a blocking problem" else empty end)
     ]) as $wait

  | ((.approved_sha // "") != "" and .approved_sha == $head) as $approved
  | ($pr.files // []) as $files
  # Per-file counts come from the REST listing; without them every line counts.
  | (if ($files | length) > 0 and ($files | all((.additions | type) == "number" and (.deletions | type) == "number"))
     then [$files[] | select(.path | not_code | not) | .additions + .deletions] | add // 0
     else ($pr.additions // 0) + ($pr.deletions // 0) end) as $code_lines
  | ((.domain // "") | tostring) as $domain

  | ([ (if ($labels | index("review:concerns")) then "the adversarial review raised concerns" else empty end),
       (if $fork then "comes from a fork" else empty end),
       (if $domain | IN("clear", "complicated") then empty
        elif $domain | IN("complex", "chaotic") then "the adversarial review calls the change \($domain)"
        else "the adversarial review names no domain for the change" end),
       (if ($trusted | index($author)) or $pipeline then empty
        else "\($author | safe) is not a trusted author" end),
       ([$pr.files // [] | .[] | .path, (.previous_filename // empty) | select(sensitive) | safe] | unique
         | if length > 0 then "touches \(.[0:5] | join(", "))\(if length > 5 then " and \(length - 5) more" else "" end)" else empty end),
       (if ([$files[] | .path | select(test("\\.go$") and (not_code | not))] | length) > 0
           and ([$files[] | .path | select(is_test)] | length) == 0
        then "changes Go code without changing a test" else empty end),
       (if $code_lines > max_code_lines then "changes \($code_lines) lines of code (over \(max_code_lines), tests and docs not counted)" else empty end),
       (($files | length) as $n
         | if $n > max_files then "changes \($n) files (over \(max_files))" else empty end),
       (if (($pr.title // "") | test("^[a-z]+(\\([^)]*\\))?!:"))
           or (($pr.body // "") | test("(?m)^BREAKING[ -]CHANGE:"))
        then "is a breaking change" else empty end),
       (if ($labels | index("needs-human")) then "is labelled needs-human" else empty end)
     ]) as $human

  | {number: $pr.number, head: $head}
  + if $skip != null then {decision: "skip", reasons: [$skip]}
    elif ($wait | length) > 0 then {decision: "wait", reasons: $wait}
    elif $approved then {decision: "merge", reasons: ["the maintainer approved \($head[0:7])"]}
    elif ($human | length) > 0 then {decision: "human", reasons: $human}
    elif .auto == "true" then {decision: "merge", reasons: ["green CI, a clean adversarial review of \($head[0:7]), and nothing that needs a human"]}
    else {decision: "human", reasons: ["low risk, but auto-merge is off (MERGE_LOOP_AUTO)"]} end
' <<<"$input"
