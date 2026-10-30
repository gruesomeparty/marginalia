#!/usr/bin/env bash
# Turns an adversarial review verdict into the edits to make on the PR.
#
# Usage (what .github/workflows/review-prs.yml does):
#   jq -n --argjson pr "$(gh pr view N --json number,headRefOid,labels)" \
#         --argjson review "$(cat review/N.json)" --arg sha "$REVIEWED_SHA" \
#         --arg now "2026-10-29" \
#         '{pr: $pr, review: $review, reviewed_sha: $sha, now: $now}' \
#     | scripts/apply-review.sh
#
# The review agent only reads, and what it read — the diff, the PR's title and
# body — may have been written by anyone. So this script, not the agent,
# decides what a verdict may change:
#
#   - labels: exactly one of review:pass, review:concerns, review:blocking,
#     derived from the findings' severities (the agent cannot call a PR with a
#     blocking finding a pass, or the reverse); nothing else is touched
#   - one comment, marked by a hidden <!-- agent:review … --> block that the
#     workflow edits in place on every re-review, so a PR carries one review
#     and not a stack of them
#   - the comment names the commit reviewed, and a verdict for any other
#     commit than the one the workflow fetched is refused: a review of a head
#     the PR has since moved past must not label the new one
#   - the comment records the reviewer's Cynefin domain for the change (clear,
#     complicated, complex, chaotic) with its reasons; the merge gate reads it
#     from the hidden block, where it comes before the agent's notes so no
#     notes text can stand in for it. A domain that is missing, unknown, or a
#     clear or complicated call with no reason given is recorded as complex:
#     only a reasoned call can lower the bar, and a forgetful reviewer fails
#     safe
#
# Visible text is short and inert: @-mentions are broken so a finding cannot
# ping anyone, markdown links do not form, HTML comments cannot open or close,
# and lengths are capped.
#
# Prints {number, add, remove, verdict, domain, comment} and exits 0; exits 1 naming
# the first invalid field of the verdict, 2 on malformed input, 3 when the PR
# moved past the reviewed commit (not an error: the next run reviews it).
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "apply-review: jq is required" >&2
  exit 2
fi

input="$(cat)"
if ! jq -e '(.pr | type == "object") and (.review | type == "object") and (.reviewed_sha | type == "string")' >/dev/null 2>&1 <<<"$input"; then
  echo "apply-review: expected {pr, review, reviewed_sha} on stdin" >&2
  exit 2
fi

invalid() {
  echo "apply-review: invalid verdict: $*" >&2
  exit 1
}

r() { jq -r ".review$1 // \"\" | tostring" <<<"$input"; }

number="$(jq -r '.pr.number // ""' <<<"$input")"
[[ "$number" =~ ^[0-9]+$ ]] || {
  echo "apply-review: pr.number missing" >&2
  exit 2
}
reviewed="$(jq -r .reviewed_sha <<<"$input")"
[[ "$reviewed" =~ ^[0-9a-f]{40}$ ]] || {
  echo "apply-review: reviewed_sha is not a commit" >&2
  exit 2
}
head="$(jq -r '.pr.headRefOid // ""' <<<"$input")"
if [ "$head" != "$reviewed" ]; then
  echo "apply-review: #$number moved to ${head:-unknown} after ${reviewed:0:7} was reviewed; skipped" >&2
  exit 3
fi

[ "$(r .pr)" = "" ] || [ "$(r .pr)" = "$number" ] || invalid "verdict is for #$(r .pr), not #$number"
[ "$(r .head_sha)" = "" ] || [ "$(r .head_sha)" = "$reviewed" ] ||
  invalid "verdict is for $(r .head_sha), not the reviewed $reviewed"
jq -e '.review.findings == null or (.review.findings | type == "array")' >/dev/null <<<"$input" ||
  invalid "findings is not a list"
[ "$(jq '.review.findings // [] | length' <<<"$input")" -le 20 ] || invalid "more than 20 findings"
jq -e '[.review.findings // [] | .[] | (.severity // "" | tostring)
  | select(. != "blocking" and . != "major" and . != "minor")] | length == 0' >/dev/null <<<"$input" ||
  invalid "a severity other than blocking, major or minor"
jq -e '[.review.findings // [] | .[] | (.title // "" | tostring | length > 0)] | all' >/dev/null <<<"$input" ||
  invalid "a finding without a title"
jq -e '.review.domain_reasons == null or (.review.domain_reasons | type == "array" and length <= 5)' >/dev/null <<<"$input" ||
  invalid "domain_reasons is not a list of at most 5"

jq --arg sha "$reviewed" '
  # Visible text: one paragraph, no mentions, no markup that hides or links.
  def inert($n):
    tostring | gsub("\r"; "") | gsub("<!--"; "&lt;!--") | gsub("-->"; "--&gt;")
    | gsub("@(?<w>[A-Za-z0-9])"; "@\u200b\(.w)") | gsub("\\]\\("; "] (")
    | if length > $n then .[0:$n] + "…" else . end;
  def oneline($n): inert($n) | gsub("\n+"; " ");
  # Hidden text: must not be able to end the comment it lives in.
  def safe: tostring | gsub("--"; "- -") | gsub("\r"; "");
  def path: tostring | gsub("[^A-Za-z0-9._/@+-]"; "") | .[0:200];

  .review as $v
  | ($v.findings // []) as $f
  | ([$f[].severity]) as $sev
  | (if ($sev | index("blocking")) then "blocking"
     elif ($sev | index("major")) then "concerns"
     else "pass" end) as $verdict
  | ([.pr.labels // [] | .[].name]) as $have
  | ("review:" + $verdict) as $label

  # A reason is {file, reason}; one without a reason says nothing.
  | ([$v.domain_reasons // [] | .[] | objects | select((.reason // "" | tostring | length) > 0)]) as $why
  | (($v.domain // "") | tostring) as $claimed
  | (if ($claimed | IN("complex", "chaotic")) then $claimed
     elif ($claimed | IN("clear", "complicated")) and ($why | length) > 0 then $claimed
     else "complex" end) as $domain
  | (if $domain == $claimed then ""
     elif ($claimed | IN("clear", "complicated")) then " (called \($claimed) without a reason)"
     else " (the review did not name a domain)" end) as $domain_note
  | ($why | map("- " + (if (.file // "") != "" then "`\(.file | path)` " else "" end) + (.reason | oneline(200)))
      | join("\n")) as $why_list

  | ({blocking: 0, major: 1, minor: 2}) as $rank
  | ([$f[] | . + {r: $rank[.severity]}] | sort_by(.r)
      | map("- **\(.severity)**"
          + (if (.file // "") != "" then " `\(.file | path)"
              + (if (.line | type) == "number" then ":\(.line | floor)" else "" end) + "`" else "" end)
          + " \(.title | oneline(120))"
          + (if (.detail // "") != "" then "  \n  \(.detail | oneline(600))" else "" end))
      | join("\n")) as $list

  | {
      number: .pr.number,
      verdict: $verdict,
      domain: $domain,
      add: (if ($have | index($label)) then [] else [$label] end),
      remove: [$have[] | select(test("^review:(pass|concerns|blocking)$") and . != $label)],
      comment: (
        "<!-- agent:review v1\n"
        + "sha: \($sha)\n"
        + "updated: \(.now // "" | safe | gsub("\n"; " "))\n"
        + "verdict: \($verdict)\n"
        + "domain: \($domain)\n"
        + (if ($v.agent_notes // "") != "" then "notes: \($v.agent_notes | safe)\n" else "" end)
        + "-->\n"
        + "**Adversarial review** of \($sha[0:7]): **\($verdict)**\n\n"
        + (if ($v.summary // "") != "" then ($v.summary | oneline(500)) + "\n\n" else "" end)
        + (if $list != "" then $list + "\n\n" else "" end)
        + "Change domain: **\($domain)**\($domain_note)\n"
        + (if $why_list != "" and $domain_note == "" then $why_list + "\n" else "" end) + "\n"
        + "<sub>Automated, read-only review. It labels and comments; it never approves, blocks or merges.</sub>")
    }
' <<<"$input"
