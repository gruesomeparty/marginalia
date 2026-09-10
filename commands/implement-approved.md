---
description: Drain one issue from Marginalia's approved-for-agent queue: implement it on a branch off main and open a PR for a human to merge.
argument-hint: [issue-number]
---

Use the `implement-approved` skill to work Marginalia's `approved-for-agent`
queue. With `$ARGUMENTS` given, work that issue — but only if the triage gate
says it is ready and no pipeline PR is already open; without arguments, work the
issue the gate names next.

Follow `skills/implement-approved/SKILL.md` exactly: preflight for an open
pipeline PR, run the triage gate, branch off `main`, implement the smallest
change that meets the acceptance criterion, run the repo's own checks with CI's
pinned linter version, open the PR with `Closes #<issue>`, and stop there — the
human merges.
