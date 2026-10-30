#!/usr/bin/env bash
# Creates (or updates) every label the feedback, triage, work and review loops
# use. Idempotent; the triage and review workflows run it before they apply a
# verdict.
set -euo pipefail
repo="${REPO:-gruesomeparty/marginalia}"
create(){ gh label create "$1" --repo "$repo" --color "$2" --description "$3" --force >/dev/null; }
create agent-feedback     "1d76db" "Filed by an agent hitting a limitation"
create approved-for-agent "0e8a16" "Approved for automated implementation"
create format-support     "5319e7" "New input format request"
create customization      "fbca04" "Configuration/customization request"
create skill-gap          "c2e0c6" "Skill/workflow gap"
create bug                "d73a4a" "Something is broken"
create enhancement        "a2eeef" "New feature or request"
create docs               "0075ca" "Documentation"
create chore              "ededed" "Maintenance"
create dependencies       "0366d6" "Dependency updates"
create github-actions     "2b7489" "GitHub Actions dependency updates"
# Triage (scripts/apply-triage.sh is the only writer of these).
create triaged            "bfdadc" "Triage has seen this issue"
create agent-ready        "c5def5" "Triage: implementable from the issue alone"
create needs-info         "d876e3" "Triage: the reporter must say more"
create needs-decision     "e99695" "Triage: a call for the maintainer"
create duplicate          "cfd3d7" "Triage: duplicates another issue"
create priority:p0        "b60205" "Broken invariant or regression on main"
create priority:p1        "d93f0b" "Bug in a shipped feature, or blocks an agent"
create priority:p2        "fbca04" "Clear improvement"
create priority:p3        "fef2c0" "Nice to have"
create size:s             "c2e0c6" "One package, an afternoon"
create size:m             "bfd4f2" "A few files across one feature"
create size:l             "d4c5f9" "Cross-cutting or needs design"
# Review (scripts/apply-review.sh is the only writer of these).
create review:pass        "0e8a16" "Adversarial review: nothing found that should stop a merge"
create review:concerns    "fbca04" "Adversarial review: a real problem to fix before merging"
create review:blocking    "b60205" "Adversarial review: breaks an invariant, a boundary or main"
# Human-only.
create hold               "000000" "Approved, but the work loop must wait"
# Merge loop (scripts/merge-loop.sh is the only writer of the merge:* labels).
create needs-human        "5319e7" "Merge loop: always ask the maintainer before merging"
create merge:human        "1d76db" "Merge loop: waiting on the maintainer's review"
create merge:changes      "e99695" "Merge loop: the maintainer asked for changes"
create merge:conflict     "b60205" "Merge loop: conflicts with main; the author rebases"
