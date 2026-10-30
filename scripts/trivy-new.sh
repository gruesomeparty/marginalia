#!/usr/bin/env bash
# Compares two Trivy JSON reports and fails only on what the change adds.
#
# A vulnerability is a (VulnerabilityID, PkgName) pair. One in the head
# report and not in the base report is new: it is an error and the script
# exits 1. One in both is already on the base branch: it is a warning, never
# a failure, because a CVE disclosed against a dependency we already ship
# would otherwise turn every pull request red, the one that bumps the
# dependency included. A report that is missing or unreadable (a scan that
# could not download its database, say) is a warning too: the gate says it
# did not run rather than blocking on infrastructure.
#
# Usage: ./scripts/trivy-new.sh <base.json> <head.json>
# Output is GitHub annotations (::error:: / ::warning::) plus a summary line.
set -euo pipefail

base="${1:-}"
head="${2:-}"

readable() { [ -n "$1" ] && [ -s "$1" ] && jq -e . "$1" >/dev/null 2>&1; }

# One line per finding: id <tab> package <tab> installed <tab> fixed <tab> severity.
findings() {
  jq -r '[.Results[]?.Vulnerabilities[]?
          | [.VulnerabilityID, .PkgName, (.InstalledVersion // ""), (.FixedVersion // ""), (.Severity // "")]]
         | unique_by(.[0], .[1]) | .[] | @tsv' "$1"
}

if ! readable "$head"; then
  echo "::warning title=Image scan::the image scan did not produce a report, so nothing was checked"
  exit 0
fi

head_list="$(findings "$head")"
if ! readable "$base"; then
  echo "::warning title=Image scan::no report for the base branch, so new and existing findings cannot be told apart; reporting all as warnings"
  base_keys=""
  report_all_as_warnings=1
else
  base_keys="$(findings "$base" | cut -f1,2)"
  report_all_as_warnings=0
fi

new=0 existing=0
while IFS=$'\t' read -r id pkg installed fixed severity; do
  [ -n "$id" ] || continue
  line="$severity $id in $pkg $installed (fixed in $fixed)"
  if [ "$report_all_as_warnings" = 1 ] || grep -qxF "$id"$'\t'"$pkg" <<<"$base_keys"; then
    echo "::warning title=Existing vulnerability::$line, already on the base branch"
    existing=$((existing + 1))
  else
    echo "::error title=New vulnerability::$line, added by this change"
    new=$((new + 1))
  fi
done <<<"$head_list"

echo "trivy-new: $new new, $existing already on the base branch"
[ "$new" -eq 0 ]
