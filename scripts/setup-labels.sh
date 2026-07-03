#!/usr/bin/env bash
set -euo pipefail
repo="suTerminus/marginalia"
create(){ gh label create "$1" --repo "$repo" --color "$2" --description "$3" --force; }
create agent-feedback     "1d76db" "Filed by an agent hitting a limitation"
create approved-for-agent "0e8a16" "Approved for automated implementation"
create format-support     "5319e7" "New input format request"
create customization      "fbca04" "Configuration/customization request"
create skill-gap          "c2e0c6" "Skill/workflow gap"
create bug                "d73a4a" "Something is broken"
create dependencies       "0366d6" "Dependency updates"
create github-actions     "2b7489" "GitHub Actions dependency updates"
