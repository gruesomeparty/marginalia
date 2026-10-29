#!/usr/bin/env bash
# Shows and changes the repository variables that switch the Actions loops
# (triage, work, merge) on and off. The maintainer runs it with their own gh:
# Actions variables need admin rights on the repository, which an agent
# session does not have.
#
#   scripts/loop-vars.sh                   every loop variable: value and effect
#   scripts/loop-vars.sh on|off NAME       set a switch to true or false
#   scripts/loop-vars.sh toggle NAME       flip a switch
#   scripts/loop-vars.sh set NAME VALUE    set a login list (space-separated)
#   scripts/loop-vars.sh unset NAME        delete a variable (back to its default)
#   scripts/loop-vars.sh check             fail if the workflows read a variable
#                                          this table does not describe, or the
#                                          table names one nothing reads (no gh)
#
# REPO overrides the repository (default gruesomeparty/marginalia). Names are
# matched case-insensitively and anything not in the table is refused, so a
# typo cannot create a variable no workflow reads. The status also lists which
# arming secrets exist, by name only; gh cannot read a secret's value.
set -euo pipefail

repo="${REPO:-gruesomeparty/marginalia}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# NAME|kind|meaning when on (switch) or what the list holds (list). Every
# workflow compares a switch to the string "true", so unset means off.
table='IMPLEMENT_LOOP|switch|The Actions work loop implements approved-for-agent issues (implement-approved.yml). Off: issues are implemented in project threads.
IMPLEMENT_CHAIN|switch|A merged claude/* PR dispatches the next work-loop run (merge loop, integration automerge).
TRIAGE_AUTO_APPROVE|switch|Triage adds approved-for-agent to ready, not-large issues from trusted authors.
TRIAGE_TRUSTED|list|Logins besides the owner whose issues triage may auto-approve.
MERGE_LOOP_AUTO|switch|The merge loop merges PRs into main that nothing flags for a human. Off: every PR waits for the owner.
MERGE_TRUSTED|list|Logins besides the owner whose PRs the merge loop may merge without asking.'

# Secrets the loops need; shown present or missing so "on" is not mistaken for armed.
secrets='CLAUDE_CODE_OAUTH_TOKEN|Arms triage, review and the work loop (or ANTHROPIC_API_KEY).
ANTHROPIC_API_KEY|Arms triage, review and the work loop (or CLAUDE_CODE_OAUTH_TOKEN).
MARGINALIA_URL|With MARGINALIA_MCP_TOKEN: the merge loop asks for a decision on hosted Marginalia.
MARGINALIA_MCP_TOKEN|With MARGINALIA_URL: the merge loop asks for a decision on hosted Marginalia.
MERGE_TOKEN|The merge loop merges with it, so the merge starts push workflows itself.'

die() { echo "loop-vars: $*" >&2; exit 2; }

# row NAME prints the table row for NAME (any case), or fails.
row() {
  local want
  want="$(tr '[:lower:]' '[:upper:]' <<<"$1")"
  grep -E "^${want}\|" <<<"$table" || die "unknown variable '$1'; known: $(cut -d'|' -f1 <<<"$table" | xargs)"
}

# read_by_workflows prints every vars.NAME the workflows mention, one per line.
read_by_workflows() {
  grep -rhoE 'vars\.[A-Z0-9_]+' "$root/.github" | sed 's/^vars\.//' | sort -u
}

check() {
  local read known bad=0
  read="$(read_by_workflows)"
  known="$(cut -d'|' -f1 <<<"$table" | sort -u)"
  for n in $(comm -23 <(echo "$read") <(echo "$known")); do
    echo "loop-vars: the workflows read vars.$n but scripts/loop-vars.sh does not describe it" >&2; bad=1
  done
  for n in $(comm -13 <(echo "$read") <(echo "$known")); do
    echo "loop-vars: scripts/loop-vars.sh describes $n but no workflow reads it" >&2; bad=1
  done
  [ "$bad" = 0 ] && echo "loop-vars: the table matches the workflows ($(wc -l <<<"$known" | tr -d ' ') variables)"
  return "$bad"
}

status() {
  local vars have
  vars="$(gh variable list --repo "$repo" --json name,value)"
  echo "Loop variables on $repo"
  while IFS='|' read -r name kind meaning; do
    local value shown
    value="$(jq -r --arg n "$name" '.[] | select(.name == $n) | .value' <<<"$vars")"
    if [ "$kind" = switch ]; then
      if [ "$value" = true ]; then shown=ON; elif [ -z "$value" ]; then shown="off (unset)"; else shown="off ($value)"; fi
    else
      shown="${value:-(none)}"
    fi
    printf '  %-20s %-12s %s\n' "$name" "$shown" "$meaning"
  done <<<"$table"

  # A repository variable no workflow reads is a leftover or a typo.
  local stray
  stray="$(comm -23 <(jq -r '.[].name' <<<"$vars" | sort -u) <(read_by_workflows))"
  [ -n "$stray" ] && echo "  Set but read by no workflow: $(xargs <<<"$stray")"

  if have="$(gh secret list --repo "$repo" --json name --jq '.[].name' 2>/dev/null)"; then
    echo "Secrets (names only)"
    while IFS='|' read -r name meaning; do
      local mark=missing
      grep -qx "$name" <<<"$have" && mark=present
      printf '  %-20s %-12s %s\n' "$name" "$mark" "$meaning"
    done <<<"$secrets"
  else
    echo "Secrets: cannot list them with this gh login"
  fi
}

put() {
  echo "loop-vars: $1 = $2"
  gh variable set "$1" --repo "$repo" --body "$2" >/dev/null
}

cmd="${1:-status}"
case "$cmd" in
  status|list) status ;;
  check) check ;;
  on|off|toggle)
    [ $# -eq 2 ] || die "usage: $0 $cmd NAME"
    r="$(row "$2")"; IFS='|' read -r name kind _ <<<"$r"
    [ "$kind" = switch ] || die "$name is a login list; use: $0 set $name \"login1 login2\""
    case "$cmd" in
      on) put "$name" true ;;
      off) put "$name" false ;;
      toggle)
        now="$(gh variable list --repo "$repo" --json name,value --jq ".[] | select(.name == \"$name\") | .value")"
        if [ "$now" = true ]; then put "$name" false; else put "$name" true; fi ;;
    esac ;;
  set)
    [ $# -eq 3 ] || die "usage: $0 set NAME VALUE"
    r="$(row "$2")"; IFS='|' read -r name kind _ <<<"$r"
    [ "$kind" = list ] || die "$name is a switch; use: $0 on|off|toggle $name"
    put "$name" "$(xargs <<<"$3")" ;;
  unset)
    [ $# -eq 2 ] || die "usage: $0 unset NAME"
    r="$(row "$2")"; IFS='|' read -r name _ <<<"$r"
    echo "loop-vars: $name unset"
    gh variable delete "$name" --repo "$repo" ;;
  -h|--help|help) sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) die "unknown command '$cmd'; try: $0 help" ;;
esac
