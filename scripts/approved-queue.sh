#!/usr/bin/env bash
# Triage gate for the approved-for-agent queue.
#
# Usage:
#   gh issue list --repo gruesomeparty/marginalia --label approved-for-agent \
#     --state open --json number,title,body,labels | scripts/approved-queue.sh
#
# Prints one line per queued issue, highest priority first (a `priority:p0`…`p3`
# label from triage; none counts as p2) and oldest first within a priority, with
# a readiness verdict, and names the one to work next. An issue labelled `hold`
# is listed and skipped: it is approved, and someone asked it to wait. An issue is READY only when its body says what
# was expected and what "done" means: implementing an issue whose acceptance
# criterion nobody wrote produces a PR nobody can judge, so the pipeline stops
# and asks instead of guessing.
#
# An issue may carry one `base:<branch>` label naming the integration branch its
# PR targets (`base:feature/hosted`); without one the base is main. Only main
# and feature/* are valid bases — a PR into feature/* auto-merges on green CI,
# a PR into main waits for a human, and nothing else is allowed to exist in
# between. Two base labels, or a base outside those, is NOT READY: guessing
# which branch work belongs on is how it gets merged into a dead one.
#
# Exit codes: 0 next issue named (or queue empty, or everything on hold), 3
# queue non-empty but nothing ready, 2 bad input.
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

valid_base() {
  [ "$1" = "main" ] && return 0
  [[ "$1" =~ ^feature/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$ ]] || return 1
  [[ "$1" != *..* ]]
}

next=""
next_base=""
blocked=0
held=0
while IFS=$'\t' read -r number title expected acceptance hold bases; do
  if [ "$hold" = "true" ]; then
    printf '#%-5s HOLD       %s\n' "$number" "$title"
    held=$((held + 1))
    continue
  fi
  missing=()
  [ "$expected" = "true" ] || missing+=("expected behaviour")
  [ "$acceptance" = "true" ] || missing+=("acceptance criterion")
  base="main"
  if [[ "$bases" == *,* ]]; then
    missing+=("a single base label (has: $bases)")
  elif [ -n "$bases" ]; then
    if valid_base "$bases"; then
      base="$bases"
    else
      missing+=("a valid base (main or feature/*, got: $bases)")
    fi
  fi
  if [ ${#missing[@]} -eq 0 ]; then
    printf '#%-5s READY      %s  -> %s\n' "$number" "$title" "$base"
    if [ -z "$next" ]; then
      next="$number"
      next_base="$base"
    fi
  else
    joined="${missing[0]}"
    for m in "${missing[@]:1}"; do joined+=", $m"; done
    printf '#%-5s NOT READY  %s — missing: %s\n' "$number" "$title" "$joined"
    blocked=$((blocked + 1))
  fi
done < <(jq -r '
  def rank: [(.labels // [])[] | .name | select(test("^priority:p[0-3]$")) | ltrimstr("priority:p") | tonumber] | min // 2;
  sort_by([rank, .number])[]
  | [ .number,
      .title,
      ((.body // "") | ascii_downcase | test("expected")),
      ((.body // "") | ascii_downcase | test("acceptance")),
      ([(.labels // [])[] | .name] | index("hold") != null),
      ([(.labels // [])[] | .name | select(startswith("base:")) | ltrimstr("base:")] | join(",")) ]
  | @tsv' <<<"$input")

if [ -n "$next" ]; then
  echo "next: #$next"
  echo "base: $next_base"
  exit 0
fi
if [ "$blocked" -eq 0 ]; then
  echo "nothing ready: $held issue(s) on hold"
  exit 0
fi
echo "nothing ready: $blocked issue(s) need triage before they can be implemented" >&2
exit 3
