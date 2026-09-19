#!/usr/bin/env bash
# Builds the live demo the landing page embeds: a real `marginalia export` of
# site/demo/, so the hero is the product rather than a picture of it. The
# Pages workflow runs this, and so can anyone editing the page — the demo is
# generated, never committed, and so cannot drift from the binary.
#
# Usage: scripts/demo.sh [output] [auto|off]
set -euo pipefail

out="${1:-site/demo.html}"
diagrams="${2:-auto}"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

bin="$(mktemp -d)/marginalia"
go build -o "$bin" .

# The log lives beside the document, so the sources are staged into a scratch
# directory: the demo is rendered from the repo's copies without the export
# writing anything back next to them.
work="$(mktemp -d)"
cp site/demo/handover.md "$work/handover.md"
cp site/demo/review.yaml "$work/review.yaml"
cp site/demo/feedback.jsonl "$work/handover.md.feedback.jsonl"

"$bin" export "$work/handover.md" \
  --config "$work/review.yaml" --diagrams="$diagrams" -o "$out"
test -s "$out"

# The product's own rule, applied to its landing page: an exported review
# loads nothing. XML namespace URIs are identifiers, not requests — every
# drawn diagram carries them — so they are the one allowed match.
leaks="$(sed -E 's/xmlns(:[a-zA-Z0-9]+)?="[^"]*"//g' "$out" |
  grep -oE 'https?://[^"'"'"' )]*' | sort -u || true)"
if [ -n "$leaks" ]; then
  echo "$out gained an external reference:" >&2
  echo "$leaks" >&2
  exit 1
fi

echo "wrote $out ($(wc -c <"$out" | tr -d ' ') bytes, diagrams=$diagrams)"
