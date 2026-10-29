#!/usr/bin/env bash
# Eligibility gate for auto-merging a pipeline PR into its integration branch.
#
# Usage (what .github/workflows/integration-automerge.yml does):
#   jq -n --argjson pr "$(gh pr view N --json number,baseRefName,headRefName,headRefOid,isDraft,isCrossRepository,body,labels)" \
#         --argjson issue "$(gh issue view M --json number,state,labels)" \
#         --arg sha "$HEAD_SHA" '{pr: $pr, issue: $issue, ci_sha: $sha}' \
#     | scripts/automerge-check.sh
#
# The rule it enforces: nothing auto-merges into main — a human reviews every
# change that reaches it. A PR into a feature/* integration branch may merge
# itself once CI is green, and only when all of this holds, because each check
# closes a way for the wrong change to land unreviewed:
#
#   - base is feature/*                     never main, never anything else
#   - head is a claude/* branch in this repo a fork cannot name its way in
#   - not a draft
#   - not labelled review:blocking          the adversarial review found a
#                                           reason it must not merge
#   - CI passed on the PR's current head    not on a commit since replaced
#   - body says "Closes #M" for one issue   the issue is what was approved
#   - #M is open, approved-for-agent, and labelled base:<that same base>
#                                           approval was for this branch
#
# Prints "merge #N closes #M into <base>" and exits 0, or names the first check
# that failed and exits 1. Exit 2 on malformed input.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "automerge-check: jq is required" >&2
  exit 2
fi

input="$(cat)"
if ! jq -e '(.pr | type == "object") and (.issue | type == "object") and (.ci_sha | type == "string")' >/dev/null 2>&1 <<<"$input"; then
  echo "automerge-check: expected {pr, issue, ci_sha} on stdin" >&2
  exit 2
fi

field() { jq -r "$1 // \"\"" <<<"$input"; }

refuse() {
  echo "not merging: $*"
  exit 1
}

number="$(field .pr.number)"
base="$(field .pr.baseRefName)"
head="$(field .pr.headRefName)"
head_sha="$(field .pr.headRefOid)"
ci_sha="$(field .ci_sha)"

[ "$base" != "main" ] || refuse "#$number targets main, which only a human merges"
[[ "$base" =~ ^feature/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$ && "$base" != *..* ]] ||
  refuse "#$number targets $base, not a feature/* integration branch"
[[ "$head" == claude/* ]] || refuse "#$number comes from $head, not a claude/* pipeline branch"
[ "$(jq -r '.pr.isCrossRepository | tostring' <<<"$input")" = "false" ] || refuse "#$number comes from a fork"
[ "$(jq -r '.pr.isDraft | tostring' <<<"$input")" = "false" ] || refuse "#$number is a draft"
jq -e '[.pr.labels // [] | .[].name] | index("review:blocking") | not' >/dev/null <<<"$input" ||
  refuse "#$number is labelled review:blocking"
[ -n "$ci_sha" ] && [ "$ci_sha" = "$head_sha" ] ||
  refuse "#$number's head is ${head_sha:-unknown}, but CI passed on ${ci_sha:-nothing}"

closes="$(jq -r '.pr.body // "" | [scan("(?i)\\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?) #([0-9]+)") | .[0]] | unique | join(",")' <<<"$input")"
[ -n "$closes" ] || refuse "#$number names no issue it closes"
[[ "$closes" != *,* ]] || refuse "#$number closes several issues ($closes); one PR is one issue"

issue="$(field .issue.number)"
[ "$issue" = "$closes" ] || refuse "#$number closes #$closes, but the issue checked was #$issue"
[ "$(field .issue.state | tr '[:upper:]' '[:lower:]')" = "open" ] || refuse "#$issue is not open"
jq -e '[.issue.labels // [] | .[].name] | index("approved-for-agent")' >/dev/null <<<"$input" ||
  refuse "#$issue is not approved-for-agent"
jq -e --arg want "base:$base" '[.issue.labels // [] | .[].name] | index($want)' >/dev/null <<<"$input" ||
  refuse "#$issue is not labelled base:$base"

echo "merge #$number closes #$issue into $base"
