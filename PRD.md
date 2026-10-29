# Marginalia — PRD

**Date:** 2026-07-02
**Status:** Draft v1
**One-liner:** A document-review tool agents can hand to a human: render a
markdown/JSON/YAML file as a readable page, collect inline comments anchored to
blocks, and return them to the agent as structured feedback events.

---

## 1. Problem

Agents produce documents that humans must review: design specs, plans, PRDs,
ADRs, booking drafts, reports. Today that review happens in one of three bad
ways:

1. **In the terminal** — reading rendered-markdown-as-plaintext, replying with
   "in the third paragraph of section 5…" prose that the agent must re-anchor
   by guesswork.
2. **In one-off HTML artifacts** — hand-built per document, feedback returned
   by copy-paste, throwaway code every time.
3. **In external tools** (Google Docs, Notion) — good UX, but the feedback is
   trapped there; the agent can't consume it as structured data.

The pattern is proven and keeps being rebuilt: Black Mirror's June 2026
timebooking session used a purpose-built review UI producing `feedback.jsonl`
that an agent reconciled. The spec review for Black Mirror's platform
foundation rebuilt the same thing for a markdown spec. Every rebuild is the
same three parts: **render, annotate, export events**.

## 2. Goals

- **One command from any agent** turns a document into a reviewable page.
- **Comments are anchored** to document blocks and survive being handed back —
  the agent knows exactly which paragraph/list/code block each note targets.
- **Feedback is structured, append-only events** (JSONL), directly consumable
  by an agent without parsing prose.
- **Reusable as a skill** — any Claude Code session (or other agent harness)
  can invoke it: "render this for review, wait, then read the feedback."
- **No export step, ever.** The reviewer comments and walks away (or hits
  Done); feedback is already on disk where the agent picks it up. Any flow
  that requires the human to copy/download/paste feedback is a failure of the
  primary design, tolerated only as a last-resort fallback.

## 3. Non-goals

- Not a document editor or CMS. The source file is never mutated by review.
- Not multi-user / concurrent review. Single reviewer; no auth in the local
  modes (localhost or private artifact). Hosted mode (§5.9) authenticates at
  the edge, but still serves one reviewer.
- Not a diff/PR review tool — code review has better tools; this is for
  documents.
- No accounts, no sign-up, no multi-tenant service. Local modes persist
  nothing beyond files on disk (server mode) or localStorage (static mode);
  hosted mode (§5.9) keeps its state in a storage backend the owner runs, and
  is the one deployment the project describes.

## 4. Users and flows

**The reviewer (human):** opens a URL, reads a well-typeset document, taps any
block to attach a comment / suggested edit / question, sees a running count,
and finishes by hitting **Done** (appends a `review_done` event) — or just
walks away. Every comment is already on disk the moment it's saved; there is
nothing to export.

**The requester (agent):** invokes the skill with a file path. Skill runs the
tool, tells the human where to look, then watches `<doc>.feedback.jsonl`
(poll for the `review_done` event or file quiescence), and addresses each
event — quoting `block` + `quote` so the human can verify anchoring.
Optionally re-renders with resolution states for a second pass.

## 5. Product design

### 5.1 Rendering

- **v1 input: Markdown.** Every block-level element (heading, paragraph,
  list, code fence, table, blockquote) becomes a commentable block, and every
  **list item** is commentable in its own right (nested items too) — a reviewer
  should never have to restructure a document to make part of it reviewable.
- **v2 input: JSON/YAML/TOML.** Rendered as a collapsible tree; every node
  path is a commentable block (`$.spec.storage.paths[2]`). Key order is the
  author's, not the decoder's, and YAML comments render with the node they
  document.
- **v2 input: `.proto` schemas.** The same collapsible tree, walked over the
  schema's own declarations instead of a data tree; every declaration is a
  commentable block anchored by dotted schema path
  (`CreateOrderRequest/customer_id`, `OrderService/CreateOrder`). Parsing is
  structural, not semantic — a file that does not compile, or whose imports
  are absent, still renders.
- **v2 input: mermaid diagrams.** A `mermaid` code fence in markdown, or a
  `.mmd`/`.mermaid` file on its own, is taken apart statement by statement:
  every node, edge and subgraph of a flowchart is a commentable block anchored
  by what it connects (`client-->api`, `payments/worker-->queue`), so a note
  lands on the edge that is wrong instead of on the diagram that contains it.
  The diagram is also **drawn**, when mermaid's own CLI (`mmdc`) is installed:
  rendered to SVG on the server, inlined into the page, and stamped with those
  same anchors, so the arrow you click in the picture is the statement your
  note lands on. Source is one toggle away, remembered per reviewer. Rendering
  server-side to SVG is what keeps the rest of the design intact — no script
  in the page, no request on open, so a shared file still survives a strict
  CSP — and the reviewer's theme and mode dress the picture by rewriting
  mermaid's own stylesheet into the page's CSS variables as the SVG comes out,
  since the render cannot know which one they will pick. Without `mmdc` the page shows
  the anchored source exactly as before: nothing in the review loop depends on
  a renderer being installed. A fence keeps its own block, so a note about the
  diagram as a whole still has somewhere to live, and a diagram type the
  parser does not take apart stays reviewable as the source block it is.
- Readable defaults: ~68ch column, serif body, sans headings, mono code,
  system fonts only (no webfont dependency), responsive down to phone width.
  The mono stack prefers a programming font the reviewer may already have
  installed (JetBrains Mono, Fira Code, Cascadia Code, Iosevka) with
  contextual ligatures on, and falls back to `ui-monospace` — nothing is ever
  fetched. Ligatures are opt-outable: they can hide a real `!`.
- **Code is highlighted at render time, on the server** (`internal/highlight`):
  fenced code blocks and `.proto` declarations get comments, strings, numbers
  and keywords spanned. A client-side highlighter would mean a script from a
  CDN, which is the one thing the page may not have. A language the tokenizer
  does not know renders as plain text rather than badly.
- **Themes are variable sets, light/dark is a mode** — one mechanism, so the
  palette an agent served (`serve --theme catppuccin-mocha`) and the reviewer's
  own choice cannot disagree. The reviewer's choice of light/dark and
  ligatures lives in their browser (`localStorage`, guarded — storage throws in
  a private window), never on the agent's disk: a display preference is not
  review state. An unknown theme name is a feature request, not a typo, so it
  hits the advertise-on-error path.
- The page is fully self-contained (inline CSS/JS) in both modes — static
  mode must survive strict CSP (no external requests).

### 5.2 Block anchoring

Each block gets a stable ID with three components:

```
{ "block": "5.3/2",                  # section path + block ordinal
  "quote": "first ~90 chars of the block's text…",
  "hash": "sha256(normalized block text)[:12] " }
```

- `block` is human-readable and cheap to re-locate.
- `quote` makes every feedback event self-describing even if the document
  moved on.
- `hash` lets a re-render mark comments as **stale** (block text changed since
  the comment was written) instead of silently misanchoring.

### 5.3 Feedback events

Append-only JSONL, one event per line, next to the source document
(`<doc>.feedback.jsonl`). Schema (aligned with Black Mirror's consumer-generic
feedback shape, deliberately — same event philosophy, independent tool):

```json
{ "doc": "specs/2026-07-02-foundation.md",
  "block": "5.3/2",
  "quote": "A registry-backed contract mirroring…",
  "hash": "9f2c01ab54de",
  "type": "comment | suggest_edit | question | approve | reject | <a configured action>",
  "text": "the reviewer's note (for suggest_edit: the replacement text)",
  "fields": { "severity": "high" },
  "author": "berkay",
  "ts": "2026-07-02T15:04:05Z" }
```

`type` is the review's vocabulary: the five built-ins, plus whatever actions
the requesting agent configured (§5.8). `fields` carries the structured values
such an action collected and is omitted when there are none, so a log written
without a config reads exactly as it always did.

Rules: events are never rewritten or deleted on disk; later events on the same
block override earlier ones when materializing a resolution view; replay is
chronological per block.

The resolution view (`GET /api/resolution`, and the page's own rendering) is
that materialization: per block, the note that stands plus its history, each
note flagged **stale** when the block's hash has changed since it was written,
and notes whose block no longer exists surfaced as **orphaned** rather than
dropped. Staleness and orphaning are readings of the log against the current
document — never fields written back into it.

### 5.4 Mode

**Server mode is the product.** `marginalia serve <doc> [--port 8787]
[--open]`: every saved comment POSTs to `/api/feedback` and is appended to
`<doc>.feedback.jsonl` on disk immediately — the agent picks feedback up
directly, even mid-review. Endpoints: `GET /` (page), `GET /api/doc` (doc +
existing events, so reopening the page restores state from disk, not
localStorage), `POST /api/feedback`, `GET /api/feedback`. A **Done** button
appends a `review_done` event — the agent's signal to proceed.

Phone/remote review is still server mode: the server binds on the Tailscale
interface and the phone opens the same URL. No second implementation.

When the agent and the reviewer share no filesystem at all — an agent in a
Claude project or a cloud session, a reviewer on a phone — neither binding
nor sharing a path works, and that case is **hosted mode** (§5.9): the same
page and the same events, served by one always-on instance that agents
publish *content* to. It is additive and opt-in; `serve` stays the default.

**Static export exists for sharing, not for local work** (`marginalia export
<doc> -o review.html`): a self-contained review page you can send to someone
who isn't on your mesh and doesn't run the tool — a colleague reviewing a
spec, the CTO reading a proposal. It's the one place a manual export button
exists (their comments come back as a `.json` you feed to `marginalia import
review.json`, converging on the canonical JSONL). The agent-facing local loop
never uses it.

What the shared page has to survive, and how:

- **It loads nothing.** One file, inline CSS and JS, and every image
  reference in the document rewritten to say what it was rather than fetch it
  — an `![](https://…)` would otherwise call out on open, from a machine and
  to a host neither side chose. A `data:` URI is already self-contained and
  is left alone.
- **No Clipboard API, ever.** Hosted and embedded contexts block it, so the
  export path is a pre-selected textarea (⌘C/Ctrl+C) plus a download wrapped
  in try/catch that says so when the viewer refuses it.
- **The browser is the store.** Comments live in `localStorage` under a key
  naming the document, replayed on load, with every access guarded: storage
  throws in a private window, and the page still has to work — just without
  remembering, and it says so.
- **Import is append-only and idempotent.** `import` skips events already in
  the log (block, type, text, author, timestamp), so merging the same file
  twice changes nothing, and an event with nothing to anchor to is refused
  before anything is written rather than half-merged.
- **The document is never touched**, in either direction: export renders a
  copy, import only appends to the log beside it.

### 5.5 Skill interface

Ships with a `SKILL.md` so agents use it uniformly:

1. Run `marginalia serve <doc> --open`.
2. Tell the human the URL and what's being asked of them.
3. Watch `<doc>.feedback.jsonl` for the `review_done` event (fallback: file
   quiescence or the human saying "done" in conversation).
4. Read the JSONL; group by block; address every event explicitly, quoting
   `block` + `quote`; mark hash-stale comments as such.
5. If the document is revised, offer a re-render; prior comments show as
   resolved/stale on the new page.

### 5.6 UI requirements

- Tap/click block → composer with type chips (comment / suggest edit /
  question); "suggest edit" prefills the block's current text.
- Comment markers visible in the margin; comment count + export/clear in a
  sticky bar; existing comments editable/deletable before export.
- Keyboard accessible (blocks focusable, Enter opens composer); visible focus
  states; `prefers-reduced-motion` respected.
- Phone-usable: same page, no separate mobile build.
- **Never depend on the Clipboard API.** Hosted/embedded contexts block it via
  permissions policy (learned 2026-07-02 reviewing the Black Mirror spec in a
  hosted artifact). Any copy affordance must degrade to a pre-selected
  textarea + manual Cmd+C; downloads wrapped in try/catch. (Server mode avoids
  the whole class: feedback goes to disk, nothing passes through a clipboard.)

### 5.7 Multi-document sessions

One `serve` may hand over a set — a directory, several paths, or a
`.marginalia.yml`-curated list (title, order, labels; it is also a whitelist, so
exclusions are reported at startup and a missing entry is a hard error). Each
document keeps its own `<doc>.feedback.jsonl`; the page shows a navigation tree
of the set with live comment counts and per-document done ticks. Done is
per-document, and a session-level Done appends a `review_done` (with
`text: "session"`) to every log, so an agent watching any one document sees the
handover close.

### 5.8 Review configuration

The requesting agent frames the review at serve time
(`serve <doc> --config review.yaml`): a title, instructions rendered as a
banner above the document, the **vocabulary** the reviewer answers in, and
which blocks are read-only.

```yaml
title: Ingest spec — retry policy
instructions: |
  Focus on the retry policy in §1. Ignore prose.
actions:
  - type: blocker      # written to the log verbatim
    label: Blocker     # button text
    key: b             # optional keyboard shortcut
  - type: finding
    requires_text: true
    fields:
      - name: severity
        options: [high, medium, low]
        required: true
builtins: true         # false replaces the built-in five outright
readonly: ["2"]        # a block and everything under it
require_verdict: false # withhold review_done until every block is answered
```

Why buttons and not just configurable types: most review feedback is a
verdict, not prose. An action with nothing to fill in is **one tap** — if
saying "nit" means opening a composer and typing, a forty-block document is a
slog and the reviewer stops being thorough, which matters most on a phone over
Tailscale where typing is the expensive part.

The same channel carries the requester's own guidance:

```yaml
notes:
  - doc: spec.md   # which document; block ids repeat across a set
    block: "1/2"
    text: why 500? I took it from the queue's batch limit — is that right?
skip:
  - "2"      # generated appendix: folded, and refused if something posts anyway
```

A note renders beside the block it names, marked as coming from the requester —
framing that survives the switch from chat to browser, where the human actually
is. Answering it is an ordinary feedback event anchored to that block, so the
loop closes without a second channel. `skip` is `readonly` plus a fold: a
sixty-block document where a third is under review should open on that third.
A note whose block the document no longer has is surfaced in the banner rather
than dropped — a question the agent asked is worth more than tidy markup.

Rules this has to keep:

- **The server is the gate, not the page.** A type that is not in the
  configured set is refused (400), an event for a read-only block is refused
  (403), a field that was not declared or a choice outside its options is
  refused — a tab left open across a restart must not be able to write
  vocabulary the agent never asked for.
- **The vocabulary is discoverable.** `GET /api/doc` echoes the configured
  actions, the requester's notes, and the read-only and skipped blocks
  resolved against the document — so the agent reading `blocker` out of the
  log can see what was asked for, how it was labelled, and what was
  deliberately not reviewed.
- **Requester-supplied framing is never a feedback event.** Instructions
  arrive at serve time and stay out of `<doc>.feedback.jsonl`: that log is the
  human's answers, not the agent's questions.

### 5.9 Hosted mode

**Why it exists.** Everything above assumes the agent and the reviewer share a
machine, or at least a mesh: `serve` takes a path, stdio `mcp` takes paths
under `--root`, and the feedback log lives beside the document. An agent in a
Claude project, a cloud session or a CI job has no filesystem in common with
the reviewer, so it cannot hand a document over at all. Hosted mode is one
long-running instance, behind a hostname the owner controls, that such agents
publish to and the owner reviews from. It is **additive and opt-in**:
`marginalia host` is a separate subcommand, local `serve` and stdio `mcp` stay
the default product, and nothing in the local loop changes.

**Content in, not paths.** Agents publish document bytes plus a name (the
extension selects the format, exactly as a path's would) and get back a
review URL. The server stores the bytes as an immutable **snapshot**.
Re-publishing to an existing review appends a new **revision**; earlier
revisions stay as written. A snapshot is never edited — that is how "never
mutate the source document" reads when the source arrived over the wire.
Prior notes re-anchor against the newest revision through
`feedback.Materialize`, so a note whose block changed reads stale and one
whose block is gone reads orphaned, the same as after a `--watch` re-parse.
A review's page always renders its newest revision.

**Storage is an interface, not a filesystem** (`internal/storage`, §6). Two
primitives, matching the two kinds of state Marginalia has:

- **Immutable blobs** — put-if-absent, get, list by prefix. Snapshots and
  review metadata are blobs; an existing key cannot be overwritten through
  the API, so metadata that changes is written as a new versioned key, never
  by rewriting the old one.
- **Append-only logs** — append one event (durable before the call returns),
  read all in order, and a cheap change token for waits. There is **no**
  rewrite, truncate or delete of a line in the API at all. The append-only
  invariant becomes structural instead of a convention.

plus one **prefix move** for archiving, which relocates whole reviews and
never edits what it moves. **Disk is the default backend** everywhere and
keeps today's `<doc>.feedback.jsonl` byte for byte: the local modes do not
change their files. **R2 is the first remote backend**: each event is its own
object, so an object store with no append still yields an append-only log.
"No database" still holds — blobs in, events out, nothing to migrate. Source
documents read by `serve`, `--watch`'s polling and the diagram cache are the
user's own files or disposable caches, not Marginalia's state, and stay on
plain `os` calls.

**Target deployment.** A single **Cloudflare Container** (Workers paid plan)
running the released image, **R2** for state, and **Cloudflare Access** at
the edge for both the human (login) and the claude.ai connector (as OAuth
authorization server). Cloudflare only, no other cloud. **Single instance,
single writer, by design**: the process loads every review from the store at
startup, keeps that state in memory, and writes through on every change, so
reads, waits and the inbox never poll the store. Billed store operations are
a design input — one PUT per comment, one listing per startup, nothing
periodic. This repo ships the binary, the image, the docs and a **generic
deployment template**: `deploy/cloudflare` is a Worker in front of one
Container (`max_instances: 1`), and its image tag moves with each release.
It ships nothing account-specific. The account, the hostname, the bucket,
the Access applications and every secret belong to the operator: the
hostname is a deploy argument, and the settings are a local file uploaded
as Worker secrets. They are kept on the operator's machine, or in a private
repository of their own. *Always-on* means always reachable, not always
running: the Container sleeps when idle and a request wakes it. An
in-flight request, a waiting agent's included, keeps it awake. A wake costs
the startup load, which is one listing plus a read of each open review.

**Identity split.** Two kinds of caller, resolved per request before any
handler runs:

- **Human** — authenticated at the edge; the instance verifies the edge's
  JWT (Cloudflare Access sends one per request) against the issuer's JWKS,
  with issuer, audience and expiry checked. Trusting the header unverified is
  not an option: the container port may be reachable around the edge.
- **Agent** — a bearer token or OAuth access token on `/mcp`.

The server enforces which event types each may append. **An agent may answer
and claim, never decide**: agents append `reply` and `addressed` only, on
every surface, exactly as the stdio tools allow today. Humans append
everything the review config allows, including `review_done`, `confirm` and
`reopen`. `author` is the authenticated identity, assigned by the server and
never taken from the request body. An agent credential presented to a page
route is refused.

**Surfaces.** One listener: `/` the inbox of pending reviews (newest activity
first, with what each one is waiting on), `/r/{id}/…` the review pages and
their API exactly as `serve` mounts them at `/`, `/mcp` the MCP tools over
Streamable HTTP, and `/healthz` for the platform. Review ids are random and
unguessable, but they are not the auth boundary — the edge is.

**Cost control.** Hard caps on what an agent can publish — document size,
documents per review, revisions per review, publishes per agent per hour,
open reviews, total stored bytes — all configurable, with conservative
defaults, and every refusal names the limit it hit so the agent can tell the
human instead of retrying. Finished reviews are archived after a retention
period by moving them, whole, under an `archive/` prefix: out of the inbox,
never deleted. A human's note is never refused by a budget.

**Non-goals.** Multi-tenant SaaS, user accounts or sign-up, a second UI,
horizontal scaling, and any storage backend beyond disk and R2 in this round.

## 6. Architecture

- **Single Go binary**, cobra subcommands (`serve`, `export`, `import`,
  `mcp`, `host`, `version`). Embedded HTML template via `go:embed`.
- Markdown parsing: goldmark with an AST walker that assigns block IDs +
  hashes at render time (the same walker feeds both modes).
- No database. Documents in, HTML out, JSONL beside the document.
- **Persistent state goes through `internal/storage`** (§5.9): immutable blobs
  (put-if-absent, get, list by prefix), append-only logs (append, read all,
  change token — no rewrite, truncate or delete), and a prefix move for
  archiving. Two backends: **disk**, the default, which writes
  `<doc>.feedback.jsonl` byte-identically to before the interface existed and
  is what every local mode uses; and **R2**, one object per event under a
  time-ordered key, written put-if-absent, so the log is append-only by
  construction. A shared conformance suite runs against both. The source
  document, `--watch` polling and the diagram cache are not state and stay on
  `os`.
- **Hosted mode** (`marginalia host`) is one process serving many reviews:
  state loaded from the store at startup and kept in memory, written through
  on every change, one instance, one writer. `mcp --http` serves the same MCP
  tools over Streamable HTTP with a bearer token or OAuth access token.
- **Reference implementation to generalize from:** Black Mirror's
  `cmd/blackmirror/timebooking_review.go` + `timebooking_review.html`
  (event-sourced review server, atomic writes, replay logic) and the
  2026-07-02 spec-review artifact (static mode with localStorage + export).
  Marginalia is those two, made document-generic and moved out of Black
  Mirror.

## 7. Distribution

- Own repo (`marginalia`), Go module, GitHub releases via goreleaser (same
  pipeline pattern as black-mirror).
- The skill folder (`skills/review-doc/`) lives in the marginalia repo and is
  installable as a Claude Code plugin, so any project can `/review-doc` a
  file.

## 8. Agent feedback loop (the tool improves itself)

Marginalia's primary users are agents. When an agent hits a limitation — an
unsupported input format (reStructuredText, say), a missing flag, a customization that doesn't
exist — that moment is the feature-request pipeline. The repo ships the
tooling to capture it:

### 8.1 Discovery

- The **binary advertises the path in its error messages**: unsupported input
  or unknown flags exit with a message like
  `.rst is not supported yet — agents: invoke the marginalia:request-feature
  skill to file it`, including the tool version. (TOML was the original
  example; the loop closed it — issue #2 shipped it.) The agent never has to know
  in advance that the channel exists; the failure itself routes them there.
- The main `review-doc` SKILL.md ends with the same pointer for softer gaps
  ("works, but I needed X").

### 8.2 The `request-feature` skill

A second skill in the repo. An agent following it:

1. **Dedupes first** — `gh issue list --label agent-feedback` + search; if an
   existing issue covers the gap, add a comment with the new context/use case
   instead of filing a duplicate.
2. **Files a structured issue** using the repo's issue form
   (`.github/ISSUE_TEMPLATE/agent-feedback.yml`): what was attempted (exact
   invocation), what was expected, actual behavior/error, workaround used (if
   any), marginalia version, and the requesting context (which project/harness
   hit it).
3. **Labels it** `agent-feedback` + a category (`format-support`,
   `customization`, `skill-gap`, `bug`).

Guardrails: no secrets or private document content in issue bodies (quote
structure, not content); at most one issue per limitation per session; the
skill instructs agents to link the session's purpose, not paste transcripts.

### 8.3 Automated implementation

Filed issues are proposals, not work orders — Berkay triages. Approval is a
label (`approved-for-agent`), which is the contract for automation:

- A scheduled agent session (or manually invoked one) picks up
  `approved-for-agent` issues, implements on a branch, opens a PR that
  references the issue, and runs the test suite. Human merge gate stays.
- Issue forms are written so an agent can implement from the issue alone —
  reproduction command, expected behavior, acceptance criterion. If a triaged
  issue lacks these, triage adds them before labeling.

**Triage loop.** `.github/workflows/triage.yml` runs
`skills/triage-issues/SKILL.md` on every new issue: type, priority, size, a
readiness verdict, and — when ready — the expected behaviour, acceptance
criterion, paths and plan, written into a hidden `<!-- agent:triage … -->`
block in the body so the issue stays short for a human and complete for an
agent. The triage agent only reads; `scripts/apply-triage.sh` applies its
verdict and owns which labels it may change. It may add `approved-for-agent`
only when the repository opts in (`TRIAGE_AUTO_APPROVE`), and only for a
ready, not-large issue by a trusted author — Berkay's gate stays the default,
and the merge gate into `main` is never delegated.

This mirrors the capture/triage split proven in the forge-feedback workflow:
agents capture freely, the human gates what becomes work, agents execute the
approved queue.

**Implementation.** The execute stage is a skill, not a bespoke service:
`skills/implement-approved/SKILL.md`, invocable as
`/marginalia:implement-approved` or dispatched by
`.github/workflows/implement-approved.yml`. `scripts/approved-queue.sh` is the
mechanical triage gate — an issue is workable only if it states an expected
behaviour and an acceptance criterion, and the gate exits 3 when nothing in the
queue qualifies, so a run stops and asks instead of inventing a definition of
done. The pipeline works **one issue at a time, one PR based on the issue's
base**, never stacks on another pipeline branch, never merges, and after a merge
verifies the code reached its base and the issue closed rather than trusting the
pull request's own state. It runs on approval, on a pipeline PR's merge, on a
weekday schedule and by hand, and is armed by a repository secret
(`CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`): unattended write access is a
deliberate choice, made by adding the credential, not a default.

**Review loop.** Every pull request, whoever opened it, gets an adversarial
review (`skills/review-pr/SKILL.md`, `.github/workflows/review-prs.yml`): an
agent looks for the reason it should not merge — an invariant broken, a trust
boundary crossed, a claim the diff does not meet — and the result is one
`review:pass|concerns|blocking` label and one comment, refreshed as the PR
moves. The reviewing agent only reads and `scripts/apply-review.sh` applies its
verdict, as with triage; external PRs are reviewed without ever running their
code or exposing a secret to them. The review informs Berkay and the work loop;
it never merges, approves or replaces the human gate on `main`.

**Integration branches.** The rule is *no change reaches `main` without
Berkay's review or the merge gate's leave* (below) — not *every step waits for
Berkay*. A larger piece of work
(hosted mode, #91) is a chain of issues each labelled `base:feature/<name>`:
their PRs target that feature branch, and `integration-automerge.yml` merges
each one there on green CI, closes its issue, and — only with the repository
variable `IMPLEMENT_CHAIN=true` — dispatches the next run. The merge decision
is `scripts/automerge-check.sh`, tested in `internal/pipeline`: base must be
`feature/*` (never `main`), head a `claude/*` branch in this repository, not a
draft, CI green on the PR's current head, and the PR closes exactly one open
issue that is `approved-for-agent` and labelled for that same base. The
feature branch reaches `main` through one PR a human reviews. This replaces a
stack, which is what it is for: every PR is based on a real, merged branch, a
review fix is a follow-up PR rather than a rebase of everything above it, and
squash merges cannot orphan the next layer.

**The merge loop.** Not every PR into `main` needs a human; the merge loop
(`merge-prs.yml`) decides which do, and Marginalia is how it asks. A tested
script (`scripts/merge-gate.sh`), not a prompt, makes the call from what
cannot be written by whoever opened the PR: green CI on the head, the
adversarial review's label for that same head, the author, the paths touched,
the size. Low-risk PRs merge on their own only when the repository opts in
(`MERGE_LOOP_AUTO`); everything else — concerns, outsiders, forks, the files
that decide how the project is built, released, deployed or trusted, large or
breaking changes — is published to the hosted instance as a review whose
vocabulary is the decision (*Merge it*, *Request changes*), and the loop
merges the exact commit the maintainer reviewed, or posts the notes back to
the PR. It is the tool reviewing its own pipeline: the agent hands the human a
document and acts on the structured answer.

## 9. Milestones

1. **M1 — server mode for markdown.** `serve` + JSONL + `review_done` +
   skill doc. This replaces the hand-built spec-review flow end-to-end.
   Includes the feedback scaffold from day one: `request-feature` skill,
   issue form, labels, advertise-on-error messages.
2. **M2 — revision loop.** Hash-stale detection, resolution view, re-render
   with prior comments displayed.
3. **M3 — trees.** Node-path anchoring, collapsible rendering: `.proto`
   schemas, then JSON/YAML/TOML data.
4. **M4 — automated implementation.** The `approved-for-agent` pipeline:
   agent picks up triaged issues, implements, opens PRs.
5. **M5 — share mode.** Static `export` + `import` for handing a review page
   to people outside your machine/mesh; artifact-safe (strict CSP).
6. **M6 — hosted mode** (#91, §5.9). One always-on instance agents publish
   to over remote MCP, reviewed from an inbox, state in R2, Cloudflare Access
   at the edge. In order, each building on the last: this spec (#92);
   `mcp --http` with a bearer token (#93); `internal/storage` with the disk
   backend and conformance suite (#94); `marginalia host` (#95);
   `publish_document` with revisions (#96); human-versus-agent identity and
   event-type gating (#97); OAuth resource server on `/mcp` for the claude.ai
   connector (#98); the inbox (#99); caps and archiving (#100); the R2
   backend (#101); the container image (#102); notifications (#103).

## 10. Open questions

- ~~Should `suggest_edit` events be auto-applicable (agent applies the
  replacement text verbatim when hash still matches)?~~ **Yes, and the tool
  never applies them itself** (#6). `marginalia suggestions <doc|dir>[--json]`
  reports the replacements that are provably safe — the note that stands for its
  block, with a hash that still matches, and a hash present at all — and reports
  everything else with the reason it needs confirmation. Applying belongs to
  whoever owns the document, because *never mutate the source document* (§5.6)
  outranks the convenience of doing it here.
- ~~Watch mode (`serve --watch`: re-render on file change mid-review)?~~ **Yes**
  (#7). Polls size+mtime (filesystem notifications miss the temp-file rename
  editors save with), keeps the last good parse if a save leaves the file
  unreadable, and re-anchors prior feedback through the resolution view — a note
  on a block that changed comes back stale for free. The page is told the
  revision it was rendered from and offers a reload rather than discarding a
  half-typed comment.
- Source files as a reviewable input (blocks per symbol, `marginalia serve
  handler.go`)? Split out of the presentation roundup into #31; highlighting
  code inside a document is done, code *as* a document is not.
- ~~Multi-document sessions (review a spec + its plan together)?~~ Answered
  in §5.7: one server, a page per document, per-document and session `review_done`.
