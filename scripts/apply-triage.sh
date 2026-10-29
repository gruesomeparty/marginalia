#!/usr/bin/env bash
# Turns a triage verdict into the edits to make on the issue.
#
# Usage (what .github/workflows/triage.yml does):
#   jq -n --argjson issue "$(gh issue view N --json number,body,labels,author)" \
#         --argjson triage "$(cat triage/N.json)" \
#         --argjson auto_approve false --argjson trusted '["gruesomeparty"]' \
#         --arg now "2026-10-29" \
#         '{issue: $issue, triage: $triage, auto_approve: $auto_approve, trusted: $trusted, now: $now}' \
#     | scripts/apply-triage.sh
#
# The triage agent only reads. It writes its verdict to a file, and this
# script, not the agent, decides what that verdict is allowed to change —
# because the issue it read may have been written by anyone, and an agent that
# could label freely could be talked into approving itself or naming a base
# branch that auto-merges. So the labels it can touch are a fixed set:
#
#   type          bug, enhancement, format-support, customization, skill-gap, docs, chore (added, never removed)
#   priority:p0…p3, size:s|m|l   exactly one of each, replaced on re-triage
#   triaged, agent-ready, needs-info, needs-decision, duplicate
#   approved-for-agent           added only when auto_approve is on, the verdict
#                                is ready, the size is not l, the issue is not
#                                on hold and its author is trusted; never removed
#                                (it is the human's label)
#
# base:* and hold are never touched. The agent's notes go into the issue body
# as one hidden block (<!-- agent:triage … -->), replaced in place on re-triage,
# so the issue stays readable for a human and the work loop finds the expected
# behaviour and acceptance criterion where scripts/approved-queue.sh looks. A
# visible comment is written only when a human has to answer something.
#
# Prints {number, add, remove, body, comment} and exits 0; exits 1 naming the
# first invalid field of the verdict, 2 on malformed input.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "apply-triage: jq is required" >&2
  exit 2
fi

input="$(cat)"
if ! jq -e '(.issue | type == "object") and (.triage | type == "object")' >/dev/null 2>&1 <<<"$input"; then
  echo "apply-triage: expected {issue, triage} on stdin" >&2
  exit 2
fi

invalid() {
  echo "apply-triage: invalid verdict: $*" >&2
  exit 1
}

t() { jq -r ".triage$1 // \"\" | tostring" <<<"$input"; }

type="$(t .type)"
case "$type" in
  bug | enhancement | format-support | customization | skill-gap | docs | chore) ;;
  *) invalid "type \"$type\"" ;;
esac
priority="$(t .priority)"
[[ "$priority" =~ ^p[0-3]$ ]] || invalid "priority \"$priority\""
size="$(t .size)"
[[ "$size" =~ ^[sml]$ ]] || invalid "size \"$size\""
needs="$(t .needs)"
case "$needs" in "" | info | decision) ;; *) invalid "needs \"$needs\"" ;; esac
dup="$(t .duplicate_of)"
[[ -z "$dup" || "$dup" =~ ^[0-9]+$ ]] || invalid "duplicate_of \"$dup\""
[ "$(t .issue)" = "" ] || [ "$(t .issue)" = "$(jq -r '.issue.number' <<<"$input")" ] ||
  invalid "verdict is for #$(t .issue), not #$(jq -r '.issue.number' <<<"$input")"

jq --arg type "$type" --arg priority "$priority" --arg size "$size" --arg needs "$needs" --arg dup "$dup" '
  # Text inside an HTML comment must not be able to end it.
  def safe: tostring | gsub("--"; "- -") | gsub("\r"; "");
  def line: safe | gsub("\n"; " ");
  def list($name; $xs):
    if ($xs | length) == 0 then "" else
      "\($name):\n" + ([$xs[] | "- " + line] | join("\n")) + "\n" end;

  .triage as $v
  | (.issue.labels // [] | map(.name)) as $have
  | ((.issue.author.login // "") as $a | (.trusted // []) | index($a) != null) as $trusted
  | ($v.agent_notes // {}) as $n
  | (($n.expected // "") | tostring) as $expected
  | ([$n.acceptance // [] | .[] | tostring | select(length > 0)]) as $acceptance
  | ($v.ready == true and $needs == "" and $dup == ""
      and ($expected | length) > 0 and ($acceptance | length) > 0) as $ready
  | ($ready and (.auto_approve == true) and $trusted and $size != "l"
      and ($have | index("hold") == null)) as $approve

  | ([$type, "triaged", "priority:" + $priority, "size:" + $size]
      + (if $ready then ["agent-ready"] else [] end)
      + (if $needs != "" then ["needs-" + $needs] else [] end)
      + (if $dup != "" then ["duplicate"] else [] end)
      + (if $approve then ["approved-for-agent"] else [] end)) as $add

  | ([$have[] | select(test("^(priority|size):"))]
      + ["agent-ready", "needs-info", "needs-decision", "duplicate"]
      | map(select(. as $l | ($have | index($l)) != null and ($add | index($l)) == null))) as $remove

  | ("<!-- agent:triage v1\n"
      + "updated: \(.now // "" | line)\n"
      + "type: \($type)  priority: \($priority)  size: \($size)  ready: \($ready)\n"
      + (if $needs != "" then "needs: \($needs)\n" else "" end)
      + (if $dup != "" then "duplicate_of: #\($dup)\n" else "" end)
      + (if $expected != "" then "expected: \($expected | line)\n" else "" end)
      + list("acceptance"; $acceptance)
      + list("paths"; $n.paths // [])
      + list("plan"; $n.plan // [])
      + list("risks"; $n.risks // [])
      + list("related"; $n.related // [])
      + (if ($n.notes // "") != "" then "notes: \($n.notes | safe)\n" else "" end)
      + "-->") as $block

  | ((.issue.body // "")
      | gsub("\\n*<!-- agent:triage[\\s\\S]*?-->\\s*$"; "")
      | gsub("<!-- agent:triage[\\s\\S]*?-->\\n?"; "")
      | rtrimstr("\n")) as $human

  | {
      number: .issue.number,
      add: $add,
      remove: $remove,
      body: (if $human == "" then $block else $human + "\n\n" + $block end),
      comment: (
        if $dup != "" then "This looks like a duplicate of #\($dup)."
          + (if ($v.question // "") != "" then " " + ($v.question | tostring) else "" end)
        elif $needs != "" and ($v.question // "") != "" then ($v.question | tostring)
        else "" end)
    }
' <<<"$input"
