#!/usr/bin/env bash
# Picks the Claude model for one run of a loop, from the work in front of it.
#
#   scripts/pick-model.sh triage|review|implement <gh-json
#
# stdin is the gh --json output for the PR (review: labels,files) or the issue
# (implement: labels); triage reads nothing. Prints one model id.
#
#   triage     Haiku: priority and labels.
#   review     Sonnet; Opus when the PR touches a design-heavy area (storage,
#              identity, MCP, hosted, review protocol, server, deploy, PRD) or
#              changes more than 300 lines.
#   implement  Opus; Sonnet for an issue triage sized size:s. An unknown issue
#              (the agent picks it) gets Opus.
#
# A model:opus, model:sonnet or model:haiku label on the PR or issue wins over
# all of it. Only someone with triage rights can add a label.
set -euo pipefail

haiku=claude-haiku-5-5
sonnet=claude-sonnet-5-5
opus=claude-opus-5-5

loop="${1:-}"
case "$loop" in triage|review|implement) ;; *) echo "usage: $0 triage|review|implement <gh-json" >&2; exit 2 ;; esac

if [ "$loop" = triage ]; then echo "$haiku"; exit 0; fi

json="$(cat)"
[ -n "$json" ] || json='{}'
labels="$(jq -r '.labels[]?.name' <<<"$json")"

for m in opus sonnet haiku; do
  if grep -qx "model:$m" <<<"$labels"; then echo "${!m}"; exit 0; fi
done

case "$loop" in
  review)
    lines="$(jq '[.files[]? | .additions + .deletions] | add // 0' <<<"$json")"
    deep="$(jq -r '.files[]?.path' <<<"$json" |
      grep -cE '^(internal/(storage|identity|mcpserver|hosted|review|server|feedback)/|deploy/|PRD\.md$)' || true)"
    count="$(jq '[.files[]?] | length' <<<"$json")"
    # gh lists at most 100 files; a listing that long is a big PR either way.
    if [ "$deep" -gt 0 ] || [ "$lines" -gt 300 ] || [ "$count" -ge 100 ]; then echo "$opus"; else echo "$sonnet"; fi ;;
  implement)
    if grep -qx 'size:s' <<<"$labels"; then echo "$sonnet"; else echo "$opus"; fi ;;
esac
