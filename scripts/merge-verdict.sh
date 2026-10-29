#!/usr/bin/env bash
# Reads the maintainer's decision on a pull request out of its Marginalia
# review, for the merge loop.
#
# Usage (what .github/workflows/merge-prs.yml does):
#   jq -n --argjson events "$(feedback_since over every document | jq '[.docs[].events[]]')" \
#         --arg published_at "$WHEN_THIS_REVISION_WAS_PUBLISHED" --argjson number N \
#         '{events: $events, published_at: $published_at, number: $number}' \
#     | scripts/merge-verdict.sh
#
# The review is published with .github/merge-review.yaml, whose two actions
# are the decision: merge_pr ("Merge it") and request_changes. Only events
# since the revision being decided count — a republish after a push keeps the
# log, and a "Merge it" on the old diff is not a "Merge it" on the new one —
# and only a human's: the hosted server already refuses those types from an
# agent, and this script does not trust that alone.
#
#   pending    the maintainer has not pressed Done on this revision
#   changes    a request_changes or reject anywhere, or notes with no decision
#              (finishing a review with comments and no verdict asks for work)
#   merge      at least one merge_pr and nothing asking for changes
#   undecided  Done with nothing said at all
#
# Prints {number, decision, notes, comment} and exits 0; exit 2 on malformed
# input. The comment is what the loop posts on the PR for changes: the notes,
# made inert (no mentions, no links, no HTML comments) and capped.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "merge-verdict: jq is required" >&2
  exit 2
fi

input="$(cat)"
if ! jq -e '(.events | type == "array") and (.published_at | type == "string") and (.published_at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}(\\.[0-9]+)?(Z|[+-]00:00)$")) and (.number | type == "number")' >/dev/null 2>&1 <<<"$input"; then
  echo "merge-verdict: expected {events, published_at, number} on stdin" >&2
  exit 2
fi

jq '
  def inert($n):
    tostring | gsub("\r"; "") | gsub("<!--"; "&lt;!--") | gsub("-->"; "--&gt;")
    | gsub("@(?<w>[A-Za-z0-9])"; "@​\(.w)") | gsub("\\]\\("; "] (") | gsub("\n+"; " ")
    | if length > $n then .[0:$n] + "…" else . end;
  def code: tostring | gsub("[`\n\r]"; "") | .[0:120];

  # Compared as instants, not strings: an offset or fractional seconds would
  # otherwise sort wrong. A time that does not parse never counts.
  def instant: tostring | sub("\\.[0-9]+"; "") | sub("[+-]00:00$"; "Z")
    | try fromdateiso8601 catch null;
  (.published_at | instant) as $since
  | if $since == null then error("published_at is not an RFC 3339 time") else . end
  | [.events[] | ((.ts // "") | instant) as $at | select($at != null and $at >= $since)
      | select((.author // "") | test("^agent(:|$)") | not)] as $ev
  | ([$ev[] | select(.type == "review_done")] | length > 0) as $done
  | ([$ev[] | select(.type == "request_changes" or .type == "reject")] | length > 0) as $changes
  | ([$ev[] | select(.type == "merge_pr")] | length > 0) as $merge
  | [$ev[] | select(.type | IN("request_changes", "reject", "comment", "question", "suggest_edit"))
      | {doc: (.doc // ""), block: (.block // ""), type, text: (.text // "")}] as $notes
  | {number,
     decision: (if ($done | not) then "pending"
                elif $changes then "changes"
                elif $merge then "merge"
                elif ($notes | length) > 0 then "changes"
                else "undecided" end),
     notes: $notes}
  | .comment = (
      "<!-- agent:merge-verdict v1 -->\n"
      + "**Changes requested** in the maintainer'"'"'s review"
      + (if ($notes | length) == 0 then "." else ":\n\n"
         + ($notes[0:20] | map("- **\(.type)** `\(.doc | code)`"
             + (if .block != "" then " `\(.block | code)`" else "" end)
             + (if .text != "" then " \(.text | inert(600))" else "" end)) | join("\n"))
         + (if ($notes | length) > 20 then "\n- … and \(($notes | length) - 20) more" else "" end) end)
      + "\n\n<sub>Push a fix and the merge loop asks again on the new commit.</sub>")
' <<<"$input"
