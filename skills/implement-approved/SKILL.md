---
name: implement-approved
description: Use when draining Marginalia's `approved-for-agent` issue queue — take one triaged issue, implement it on a branch off main, prove it, and open a pull request for a human to merge. Also use to check whether the queue is ready to be worked at all.
---

# Implementing the approved queue

Marginalia's feedback loop has three parts and this skill is only the third:
**capture** is open (any agent files `agent-feedback` issues via
`request-feature`), **approval** is Berkay's alone (the `approved-for-agent`
label), **execution** is this. Never implement an issue that does not carry the
label, however obvious the fix looks, and never merge your own work — the human
merge gate is the point of the split.

## Guardrails (read first)

- **One issue, one PR, then stop.** If a pipeline PR is already open, this run
  does nothing but say so.
- **Base every branch on `main`.** Never stack on another branch.
- **Never merge, never approve.** Open the PR and hand it over.
- **Quote structure, never content** in issues and PR bodies — no secrets, no
  private document text.
- `PRD.md` is the authoritative spec and `CLAUDE.md` lists the invariants that
  are easy to violate. Read both before writing code; a change that breaks one
  of those invariants is wrong even if the issue asked for it. Say so on the
  issue instead.

## 1. Preflight: is a run already in flight?

```bash
gh pr list --repo gruesomeparty/marginalia --state open --json number,title,headRefName
```

If any open PR came from a previous run of this skill, **stop** and report it.
Two open pipeline PRs means a stack, and a stack is how work gets merged into a
dead branch instead of `main`.

## 2. Triage gate: what is ready?

```bash
gh issue list --repo gruesomeparty/marginalia --label approved-for-agent \
  --state open --json number,title,body | scripts/approved-queue.sh
```

The gate prints a verdict per issue, oldest first, and names the one to work
(`next: #N`). Its exit code is the instruction:

| exit | meaning | what to do |
|---|---|---|
| 0 + `queue empty` | nothing approved | stop; say the queue is empty |
| 0 + `next: #N` | #N is implementable | work #N |
| 3 | queued issues, none ready | comment on each, then stop |

An issue is READY only when its body says what was **expected** and what
**done** means. For a NOT READY issue, comment naming exactly what is missing
and move on — never invent an acceptance criterion. An issue whose success
nobody defined produces a PR nobody can judge, and guessing turns the human's
approval into a rubber stamp.

## 3. Implement

```bash
git fetch origin main && git checkout -B claude/<short-slug>-<issue-number> origin/main
```

Implement the **smallest change that satisfies the stated acceptance
criterion**, and write the tests that encode it — the criterion, in code, is
what makes the PR reviewable in a minute rather than an hour. Don't widen the
scope; a second improvement you noticed belongs in its own issue via
`request-feature`.

## 4. Prove it before pushing

Run what CI runs, locally, and read your own diff adversarially first:

```bash
go build ./...
go test -race ./...
go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80
golangci-lint run
```

**Match CI's linter version or this step is theatre.** `.github/workflows/ci.yml`
pins `golangci-lint` (v2.12.2 at the time of writing) and a linter built against
an older Go toolchain refuses to run on this module — it exits with a version
error rather than findings, which reads like success if you are not looking.
When in doubt, build the pinned version:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
```

If the change touches the review page, also drive it in a browser: serve a
document, save a comment, and check the event landed in
`<doc>.feedback.jsonl` with the anchor you expect. The page is the product; a
green test suite has never once noticed that a comment attached to the wrong
bullet.

## 5. Open the PR

```bash
git push -u origin claude/<short-slug>-<issue-number>
gh pr create --repo gruesomeparty/marginalia --base main \
  --title "<type>: <what changed>" --body "Closes #<issue>. …"
```

The body states the acceptance criterion, how it was verified (tests, and the
browser check where relevant), and any decision you had to make that the issue
left open. Then **stop**. Do not merge, do not approve, do not open the next
issue's PR.

## 6. After the human merges

Verify the outcome rather than trusting the PR's state:

```bash
git fetch origin main
git ls-tree --name-only origin/main <paths the change added>
gh issue view <issue> --json state
```

A PR can read "merged" and still not be on `main` — that happens when it merged
into another branch — and an issue only auto-closes when its PR merges into the
default branch. If either check fails, say so plainly; that is a mis-merge, not
a formality. Then delete the head branch (which is also what retargets any PR
based on it) and start the next issue from step 1.

---

*Hit a limitation of Marginalia itself while doing this? Invoke the
`marginalia:request-feature` skill — the queue you are draining is fed by it.*
