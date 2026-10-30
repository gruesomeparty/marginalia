---
name: loop-vars
description: Use when the maintainer wants to see or flip the repository variables that switch Marginalia's Actions loops (IMPLEMENT_LOOP, MERGE_LOOP_AUTO, TRIAGE_AUTO_APPROVE and the rest), e.g. "is auto-merge on?", "turn the work loop off", "trust alice for triage".
---

# Loop variables

The triage, work and merge loops are switched by GitHub Actions repository
variables. `scripts/loop-vars.sh` is the one place that lists them, says what
each does, and changes them. Its table is checked against every `vars.NAME`
in `.github/` by `TestLoopVarsDescribesEveryVariableTheWorkflowsRead`, so a
workflow that starts reading a new variable fails CI until the table
describes it.

## Use

```sh
scripts/loop-vars.sh                          # value and effect of every variable, plus arming secrets
scripts/loop-vars.sh on MERGE_LOOP_AUTO        # set a switch to true
scripts/loop-vars.sh off IMPLEMENT_LOOP        # set a switch to false
scripts/loop-vars.sh toggle TRIAGE_AUTO_APPROVE
scripts/loop-vars.sh set TRIAGE_TRUSTED "alice bob"   # login lists, space-separated
scripts/loop-vars.sh unset MERGE_TRUSTED       # delete, back to the default
```

Names are case-insensitive. An unknown name is refused, so a typo cannot
create a variable no workflow reads. `REPO=owner/name` points it elsewhere.

## The variables

| Variable | Kind | When `true` / what it holds |
| --- | --- | --- |
| `IMPLEMENT_LOOP` | switch | The Actions work loop implements `approved-for-agent` issues. Off: issues are implemented in project threads. |
| `IMPLEMENT_CHAIN` | switch | A merged `claude/*` PR dispatches the next work-loop run. |
| `TRIAGE_AUTO_APPROVE` | switch | Triage adds `approved-for-agent` to ready, not-large issues from trusted authors. |
| `TRIAGE_TRUSTED` | list | Logins besides the owner whose issues triage may auto-approve. |
| `MERGE_LOOP_AUTO` | switch | The merge loop merges PRs into `main` that nothing flags for a human. Off: every PR waits for the owner. |
| `MERGE_TRUSTED` | list | Logins besides the owner whose PRs the merge loop may merge without asking. |

Every workflow compares a switch to the string `true`, so unset is off. A
switch only matters when its loop is armed: the status lists the secrets the
loops need (`CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`, the
`MARGINALIA_*` pair, `MERGE_TOKEN`) as present or missing, by name only.

## Rules for the agent running this

- **Who can run it.** Setting a variable needs admin on the repository. A
  project or cloud session's GitHub access normally cannot even read them
  (the proxy answers 403). Then do not retry or look for another route: give
  the maintainer the exact command to run on their machine.
- **Changing a switch is the maintainer's call.** Run `on`/`off`/`toggle`/
  `set`/`unset` only when they asked for that change in words. Showing the
  status is always fine.
- **Adding a variable.** When a workflow starts reading a new `vars.NAME`,
  add its row to the `table` in `scripts/loop-vars.sh` and to the table
  above, in the same PR.
