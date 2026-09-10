#!/usr/bin/env bash
# Triage gate for the approved-for-agent queue.
#
# Usage:
#   gh issue list --repo gruesomeparty/marginalia --label approved-for-agent \
#     --state open --json number,title,body | scripts/approved-queue.sh
#
# Prints one line per queued issue, oldest first, with a readiness verdict, and
# names the one to work next. An issue is READY only when its body says what
# was expected and what "done" means: implementing an issue whose acceptance
# criterion nobody wrote produces a PR nobody can judge, so the pipeline stops
# and asks instead of guessing.
#
# Exit codes: 0 next issue named (or queue empty), 3 queue non-empty but
# nothing ready, 2 bad input.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "approved-queue: jq is required" >&2
  exit 2
fi

input="$(cat)"
if ! jq -e 'type == "array"' >/dev/null 2>&1 <<<"$input"; then
  echo "approved-queue: expected a JSON array of issues on stdin" >&2
  exit 2
fi

if [ "$(jq 'length' <<<"$input")" -eq 0 ]; then
  echo "queue empty"
  exit 0
fi

next=""
blocked=0
while IFS=$'\t' read -r number title expected acceptance; do
  missing=()
  [ "$expected" = "true" ] || missing+=("expected behaviour")
  [ "$acceptance" = "true" ] || missing+=("acceptance criterion")
  if [ ${#missing[@]} -eq 0 ]; then
    printf '#%-5s READY      %s\n' "$number" "$title"
    [ -n "$next" ] || next="$number"
  else
    joined="${missing[0]}"
    for m in "${missing[@]:1}"; do joined+=", $m"; done
    printf '#%-5s NOT READY  %s — missing: %s\n' "$number" "$title" "$joined"
    blocked=$((blocked + 1))
  fi
done < <(jq -r '
  sort_by(.number)[]
  | [ .number,
      .title,
      ((.body // "") | ascii_downcase | test("expected")),
      ((.body // "") | ascii_downcase | test("acceptance")) ]
  | @tsv' <<<"$input")

if [ -n "$next" ]; then
  echo "next: #$next"
  exit 0
fi
echo "nothing ready: $blocked issue(s) need triage before they can be implemented" >&2
exit 3
