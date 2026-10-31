#!/usr/bin/env bash
# Names the issues waiting on an answer that have one: labelled needs-info or
# needs-decision, whose latest comment is a human's (scripts/comment-from.sh)
# and newer than the triage that asked.
#
# Usage (what the six-hourly sweep in .github/workflows/triage.yml does):
#   jq -n --argjson issues "$(gh issue list --json number,labels,body,comments …)" \
#         --argjson trusted '["gruesomeparty"]' '{issues: $issues, trusted: $trusted}' \
#     | scripts/answered-issues.sh
#
# The comment event already re-triages an answer as it is written. The sweep
# catches the ones it missed: an answer from before that event existed, or a
# run that failed. "Newer than the triage" reads the `updated:` stamp in the
# issue's <!-- agent:triage --> block, so a re-triage that still needs an
# answer does not pick the same comment again every six hours. A stamp that
# is a bare date (older blocks) counts a comment from that same day as newer:
# at most one extra run, once.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
input="$(cat)"
trusted="$(jq -c '.trusted // []' <<<"$input")"

jq -c '
  (.issues // [])[]
  | select([.labels[]?.name] | any(. == "needs-info" or . == "needs-decision"))
  | select((.comments // []) | length > 0)
  | ([(.body // "") | capture("<!--\\s*agent:triage[^\\n]*\\n(?:[\\s\\S]*?\\n)?updated: (?<at>[^\\n]*)")][0].at // "") as $at
  | (.comments | last) as $c
  | select($at == "" or ($c.createdAt // "") > $at)
  | {number, login: ($c.author.login // ""), body: ($c.body // "")}
' <<<"$input" | while read -r row; do
  from="$(jq -c --argjson trusted "$trusted" '{login, body, type: "", trusted: $trusted}' <<<"$row" | "$here/comment-from.sh")"
  if [ "$from" = human ]; then jq -r .number <<<"$row"; fi
done
