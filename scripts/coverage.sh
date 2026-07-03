#!/usr/bin/env bash
set -euo pipefail
threshold="${1:-80}"
go tool cover -func=coverage.out > coverage.txt
total="$(grep -E '^total:' coverage.txt | awk '{print $3}' | tr -d '%')"
{
  echo "### Coverage: ${total}% (floor ${threshold}%)"
  echo ""
  echo '```'
  cat coverage.txt
  echo '```'
} >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"
awk -v t="$threshold" -v c="$total" 'BEGIN{ exit (c+0 < t+0) ? 1 : 0 }' || {
  echo "coverage ${total}% is below floor ${threshold}%" >&2
  exit 1
}
