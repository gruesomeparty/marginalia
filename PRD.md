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
- Not multi-user / concurrent review. Single reviewer, no auth (localhost or
  private artifact).
- Not a diff/PR review tool — code review has better tools; this is for
  documents.
- No cloud service, no accounts, no persistence beyond files on disk (server
  mode) or localStorage (static mode).

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
- Readable defaults: ~68ch column, serif body, sans headings, mono code,
  system fonts only (no webfont dependency), responsive down to phone width.
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
  "type": "comment | suggest_edit | question | approve | reject",
  "text": "the reviewer's note (for suggest_edit: the replacement text)",
  "author": "berkay",
  "ts": "2026-07-02T15:04:05Z" }
```

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

**Static export exists for sharing, not for local work** (`marginalia export
<doc> -o review.html`): a self-contained review page you can send to someone
who isn't on your mesh and doesn't run the tool — a colleague reviewing a
spec, the CTO reading a proposal. It's the one place a manual export button
exists (their comments come back as a `.json` you feed to `marginalia import
feedback.json --doc <doc>`, converging on the canonical JSONL). The
agent-facing local loop never uses it.

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

## 6. Architecture

- **Single Go binary**, cobra subcommands (`serve`, `export`, `import`,
  `version`). Embedded HTML template via `go:embed`.
- Markdown parsing: goldmark with an AST walker that assigns block IDs +
  hashes at render time (the same walker feeds both modes).
- No database. Documents in, HTML out, JSONL beside the document.
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

This mirrors the capture/triage split proven in the forge-feedback workflow:
agents capture freely, the human gates what becomes work, agents execute the
approved queue.

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

## 10. Open questions

- Should `suggest_edit` events be auto-applicable (agent applies the
  replacement text verbatim when hash still matches)? Leaning yes; the
  resolution view now reports exactly that condition (a non-stale
  `suggest_edit`), so the remaining question is only who applies it (#6).
- Watch mode (`serve --watch`: re-render on file change mid-review)?
- ~~Multi-document sessions (review a spec + its plan together)?~~ Answered
  in §5.7: one server, a page per document, per-document and session `review_done`.
