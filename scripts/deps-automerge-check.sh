#!/usr/bin/env bash
# Eligibility gate for auto-merging a Dependabot PR into main once CI is green.
#
# Usage (what .github/workflows/deps-automerge.yml does):
#   jq -n --argjson pr "$(gh pr view N --json number,author,baseRefName,headRefName,headRefOid,isDraft,isCrossRepository,commits)" \
#         --arg sha "$HEAD_SHA" '{pr: $pr, ci_sha: $sha}' \
#     | scripts/deps-automerge-check.sh
#
# This is the one exception to "a human reviews every change that reaches
# main", and Berkay asked for it: a dependency update merges itself on green CI
# rather than waiting on him. The exception is for dependency updates only, so
# every check below is about proving that is all this PR is:
#
#   - opened by dependabot[bot]             the author is an app, not a claim
#   - head is a dependabot/* branch in this repo
#   - every commit is Dependabot's          a PR anyone else pushed to is no
#                                           longer just a version bump
#   - base is main, not a draft
#   - CI passed on the PR's current head    not on a commit since replaced
#
# Prints "merge #N (dependency update)" and exits 0, or names the first check
# that failed and exits 1. Exit 2 on malformed input.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "deps-automerge-check: jq is required" >&2
  exit 2
fi

input="$(cat)"
if ! jq -e '(.pr | type == "object") and (.ci_sha | type == "string")' >/dev/null 2>&1 <<<"$input"; then
  echo "deps-automerge-check: expected {pr, ci_sha} on stdin" >&2
  exit 2
fi

field() { jq -r "$1 // \"\"" <<<"$input"; }

refuse() {
  echo "not merging: $*"
  exit 1
}

bot="dependabot[bot]"
# gh reports an app author as "app/dependabot" or "dependabot[bot]" depending
# on the API it took the answer from; both name the same app.
is_bot='. == "dependabot[bot]" or . == "app/dependabot" or . == "dependabot"'

number="$(field .pr.number)"
base="$(field .pr.baseRefName)"
head="$(field .pr.headRefName)"
head_sha="$(field .pr.headRefOid)"
ci_sha="$(field .ci_sha)"

jq -e ".pr.author.login // \"\" | $is_bot" >/dev/null <<<"$input" ||
  refuse "#$number was opened by $(field .pr.author.login), not $bot"
[[ "$head" == dependabot/* ]] || refuse "#$number comes from $head, not a dependabot/* branch"
[ "$(jq -r '.pr.isCrossRepository | tostring' <<<"$input")" = "false" ] || refuse "#$number comes from a fork"
[ "$base" = "main" ] || refuse "#$number targets $base, not main"
[ "$(jq -r '.pr.isDraft | tostring' <<<"$input")" = "false" ] || refuse "#$number is a draft"
[ -n "$ci_sha" ] && [ "$ci_sha" = "$head_sha" ] ||
  refuse "#$number's head is ${head_sha:-unknown}, but CI passed on ${ci_sha:-nothing}"

jq -e '(.pr.commits // []) | length > 0' >/dev/null <<<"$input" || refuse "#$number lists no commits"
others="$(jq -r "[.pr.commits[] | .authors // [] | .[] | (.login // \"\") | select(($is_bot) | not)] | unique | join(\", \")" <<<"$input")"
[ -z "$others" ] || refuse "#$number has commits by $others, not only $bot"
jq -e '[.pr.commits[] | (.authors // []) | length == 0] | any | not' >/dev/null <<<"$input" ||
  refuse "#$number has a commit with no author on record"

echo "merge #$number (dependency update)"
