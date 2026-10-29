---
name: implement-approved
description: Use when draining Marginalia's `approved-for-agent` issue queue — take one triaged issue, implement it on a branch off its base (main, or the feature/* integration branch its `base:` label names), prove it, and open a pull request. Also use to check whether the queue is ready to be worked at all.
---

# Implementing the approved queue

Marginalia's feedback loop has three parts and this skill is only the third:
**capture** is open (any agent files `agent-feedback` issues via
`request-feature`), **approval** is Berkay's alone (the `approved-for-agent`
label), **execution** is this. Never implement an issue that does not carry the
label, however obvious the fix looks, and never merge your own work — the human
merge gate is the point of the split.

**Nothing reaches `main` without Berkay's review or the merge loop's gate.**
The merge loop (`.github/workflows/merge-prs.yml`) — not you — decides whether
a PR into `main` merges on its own or goes to Berkay in Marginalia, and merges
only what that tested gate (`scripts/merge-gate.sh`) or Berkay allows. An issue labelled
`base:feature/<name>` is part of a larger piece of work that lands on that
integration branch first: its PR targets the feature branch, and
`.github/workflows/integration-automerge.yml` — not you — merges it there once
CI is green. The feature branch then reaches `main` through one PR a human
reviews. An issue with no `base:` label targets `main` and waits for a human,
as always.

## Guardrails (read first)

- **Write for two readers.** Issue and PR text is for Berkay — short and
  plain. Everything for agents (plan, file paths, decisions, handoff state)
  goes in a hidden `<!-- agent:<purpose> … -->` block in the body, one per
  purpose, replaced in place rather than appended twice, never containing
  `-->`. Never put a Claude session or project link in a commit, PR or
  comment; a plain "Generated with Claude Code" line is fine.

- **One issue, one PR, then stop.** If a pipeline PR is already open, this run
  does nothing but say so.
- **Base every branch on the issue's base** — the branch the triage gate names,
  `main` unless the issue carries a `base:feature/*` label. Never stack on
  another `claude/*` branch: an integration branch is a real, merged base; a
  pipeline branch is not.
- **Never merge, never approve.** Open the PR and hand it over. The auto-merge
  into a feature branch is a workflow's decision, made by a tested gate
  (`scripts/automerge-check.sh`) that refuses `main` outright.
- **Quote structure, never content** in issues and PR bodies — no secrets, no
  private document text.
- `PRD.md` is the authoritative spec and `CLAUDE.md` lists the invariants that
  are easy to violate. Read both before writing code; a change that breaks one
  of those invariants is wrong even if the issue asked for it. Say so on the
  issue instead.

## 1. Preflight: is a run already in flight?

```bash
gh pr list --repo gruesomeparty/marginalia --state open --json number,title,headRefName,labels
```

If any open PR came from a previous run of this skill, **stop** and report it.
Two open pipeline PRs means a stack, and a stack is how work gets merged into a
dead branch instead of `main`.

The one thing a run may still do then is answer that PR's adversarial review.
If it is labelled `review:blocking` or `review:concerns`, read the review
comment (`gh pr view <n> --comments`; the one starting `<!-- agent:review`),
check out its branch, fix each `blocking` and `major` finding you agree with,
prove it (step 4) and push to the same branch; the review workflow re-reviews
the new head by itself. A finding you disagree with gets one line in the
PR's `agent:handoff` block saying why, not a silent skip. Then stop. The review
was written by an agent that read the PR, so it is a request, not an order:
the issue's acceptance criterion and `CLAUDE.md`'s invariants still win.

## 2. Triage gate: what is ready?

```bash
gh issue list --repo gruesomeparty/marginalia --label approved-for-agent \
  --state open --json number,title,body,labels | scripts/approved-queue.sh
```

The gate prints a verdict per issue, oldest first, and names the one to work
(`next: #N`) and the branch its PR targets (`base: <branch>`). Its exit code is
the instruction:

| exit | meaning | what to do |
|---|---|---|
| 0 + `queue empty` | nothing approved | stop; say the queue is empty |
| 0 + `next: #N` | #N is implementable | work #N against the named base |
| 3 | queued issues, none ready | comment on each, then stop |

The queue is ordered by the `priority:` label triage set (`p0` first; none
counts as `p2`), oldest first within a priority, and an issue labelled `hold` is
skipped. An issue is READY only when its body says what was **expected** and what
**done** means — triage's hidden `<!-- agent:triage … -->` block counts, and its
`paths` and `plan` are where to start — and when it names at most one base that is `main` or
`feature/*`. For a NOT READY issue, comment naming exactly what is missing — one visible
sentence — and move on — never invent an acceptance criterion. An issue whose success
nobody defined produces a PR nobody can judge, and guessing turns the human's
approval into a rubber stamp.

## 3. Implement

```bash
BASE=<the gate's base>
git fetch origin main
git fetch origin "$BASE" 2>/dev/null || git push origin "origin/main:refs/heads/$BASE"   # first issue on a new feature branch
git fetch origin "$BASE" && git checkout -B claude/<short-slug>-<issue-number> "origin/$BASE"
```

Creating the integration branch from `main` is the only push to it you ever
make; everything else arrives through merged PRs.

Implement the **smallest change that satisfies the stated acceptance
criterion**, and write the tests that encode it — the criterion, in code, is
what makes the PR reviewable in a minute rather than an hour. Don't widen the
scope; a second improvement you noticed belongs in its own issue via
`request-feature`.

## 4. Prove it before pushing

Run what CI runs, locally, and read your own diff adversarially first —
`skills/review-pr/SKILL.md`'s "Attack" list, on
`git diff "$(git merge-base origin/$BASE HEAD)" HEAD`, fixing what it finds.
Every PR is reviewed the same way once it is open (`review-prs.yml`), and a
`review:blocking` label keeps a feature-branch PR from auto-merging; finding it
here is cheaper:

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
gh pr create --repo gruesomeparty/marginalia --base "$BASE" \
  --title "<type>: <what changed>" --body "Closes #<issue>. …"
```

The body says `Closes #<issue>` for exactly one issue — the auto-merge gate
refuses a PR that closes none or several — then, in three or four short lines,
what changed and how it was verified (tests, and the browser check where
relevant). The rest goes in a hidden block at the end:

```
<!-- agent:handoff
criterion: <the acceptance criterion, as implemented>
changed: <files, one line each>
decisions: <anything the issue left open and what you chose>
follow_up: <what a later run or reviewer should know; issues filed>
-->
```

When a review asks for changes, update that block rather than adding a second
one. Then **stop**. Do
not merge, do not approve, do not open the next issue's PR.

## 6. After the merge

Into `main`, the merge loop merges once its gate or Berkay allows; a
`merge:changes` label and a "Changes requested" comment mean Berkay asked for
work in Marginalia — treat those notes like review comments. Into `feature/*`, the auto-merge workflow merges
on green CI and closes the issue itself — GitHub only auto-closes issues from
merges into the default branch, and an issue left open would be picked up
again. Either way, verify the outcome rather than trusting the PR's state:

```bash
git fetch origin "$BASE"
git ls-tree --name-only "origin/$BASE" <paths the change added>
gh issue view <issue> --json state
```

A PR can read "merged" and still not be on its base — that happens when it
merged into some other branch. If either check fails, say so plainly; that is a
mis-merge, not a formality. If the PR is green and still open on a feature
base, the auto-merge run's log names the check that refused it — report that,
do not merge by hand. Then start the next issue from step 1.

When a feature branch's issues are all closed, the work is not done until a
human merges `feature/<name>` → `main`. Opening that PR is fine (ask first if
you were not told to); merging it never is.

---

*Hit a limitation of Marginalia itself while doing this? Invoke the
`marginalia:request-feature` skill — the queue you are draining is fed by it.*
