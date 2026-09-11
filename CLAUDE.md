# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status: M1 + M2 + M3 shipped

The repo has a working Go module implementing PRD milestone **M1** — server mode
for markdown (`serve`, block anchoring, append-only JSONL feedback,
`review_done`) — plus full CI/CD and the installable Claude plugin (skills +
`/marginalia:review-doc`). **M3** is in: `.proto` schemas (issue #15) and
JSON/YAML/TOML trees (issue #2) render as folding trees anchored by node path,
and mermaid flowcharts anchor per statement (issue #25) — in a markdown fence
and as `.mmd`/`.mermaid` documents.
Multi-document sessions (issue #8) serve a set — a directory, several paths, or
a `.marginalia.yml`-curated list — one page per document, and markdown list
items anchor individually (issue #12). **M2** is in: `feedback.Materialize`
replays a log against the document as it now reads, so a re-render marks notes
stale, surfaces ones whose block is gone, and shows the current state per block
(issue #1).
`PRD.md` is the authoritative spec; read it before writing anything. This file summarizes it and flags the constraints that are
easy to violate. When the PRD and this file disagree, the PRD wins (and update
this file). M2–M5 are still ahead (see Build order below).

## What Marginalia is

A document-review tool an agent hands to a human. It renders a markdown (later
JSON/YAML) file as a readable page, collects inline comments anchored to blocks,
and writes them back as structured, append-only feedback events the agent
consumes directly. Three parts: **render, annotate, export events.** Its primary
users are agents, not humans.

## Planned architecture (from PRD §6)

- **Single Go binary**, cobra subcommands: `serve`, `suggestions`, `export`,
  `import`, `version`. `suggestions` reads a document and its log from disk (no
  server) and splits `suggest_edit` events into applicable and
  needs-confirmation via `feedback.Resolution.Suggestions()`. Applying requires
  all three of: the suggestion is the note that stands for its block, its hash
  matches the block now, and it has a hash at all — `Materialize` treats a
  hash-less note as not-stale, which is right for reading and not good enough
  for editing. The tool never applies anything: *never mutate the source
  document*.
- `internal/reviewset` resolves what `serve` was pointed at: one file, several,
  or a directory (walked, skipping hidden/`node_modules`/`vendor`, capped at 200
  documents). A `.marginalia.yml` in a served directory is whitelist + order +
  labels, and is then the *only* tree — so a listed-but-missing path is a hard
  error and startup reports how many supported files the index excluded.
- Server routes: `GET /` (first document), `GET /d/{rel...}` (one page per
  document), `GET /api/doc[?doc=]`, `GET|POST /api/feedback[?doc=]`,
  `POST /api/session_done`. A posted event names its document and the server
  only writes to documents in the served set — a stale page must not be able to
  append elsewhere. Per-document `review_done` comes from the page's Done
  button; `session_done` writes one to every log with `text: "session"`.
- HTML template embedded via `go:embed`; one template feeds both server and
  static modes. The page's `window.__MARGINALIA__` payload is a *projection*
  (`web.project`), not the whole `Document`: ids, anchors, text, parent and
  has-children only — kinds and levels are read off the block elements' data
  attributes, and the rendered HTML is already in the DOM. Render into a buffer
  before writing: once bytes are on the wire the status is sent, and half a
  document under a 200 is worse than an honest 500.
- `serve --watch` re-parses a document when its file changes (issue #7):
  `internal/server/watch.go` polls size+mtime every 500ms — deliberately not
  fsnotify, which loses the watch when an editor saves by renaming a temp file
  over the original — swaps the parse behind `Server.mu`, and bumps
  `Server.Revision()`. Baselines are captured in `New()` so a save between
  startup and the first tick is still caught. An unreadable or unparseable file
  keeps the last good render rather than blanking the page. `GET /api/revision`
  and the payload's `revision` let the page notice it has been outrun; it shows
  a reload prompt while a composer is open and auto-reloads only when nothing
  is in progress. Every handler reads documents through `s.docOf(entry)`.
- `Serve` waits for the shutdown drain before returning, so a comment saved as
  the reviewer closes the tab reaches disk before the process exits. POSTed
  events are capped (`maxFeedbackBody`) and answered with 413 past it.
- Markdown via **goldmark** + an AST walker that assigns each block an ID + hash at render time.
- Tree inputs share `internal/document`'s `treeBuilder`: pre-ordered flat
  blocks carrying `Parent`/`Level`/`HasChildren`, which the one template
  renders as an indented, foldable list (depth is a CSS variable per
  `data-level`, so indentation needs no script). `.proto` is parsed by
  `internal/protoschema` — a tolerant, structural proto3 parser (no protoc, no
  import resolution, unknown constructs preserved as blocks). JSON/YAML/TOML
  parse into `dataNode` (`datatree.go`) and share path derivation, rendering
  and anchoring; only the decoders differ. Key order is always the author's:
  JSON walks the token stream, YAML uses `yaml.Node`, TOML recovers order from
  `MetaData.Keys()`. Mermaid is parsed by `internal/mermaid` — line-based,
  bracket-aware and equally tolerant, returning statements with the byte range
  they occupy so a fence can be annotated in place. Nothing renders the
  diagram as a picture: that needs JavaScript (a megabyte inlined, and
  `unsafe-eval` under strict CSP) or headless Chromium, which costs the single
  Go binary.
- **No database.** Documents in, HTML out, JSONL beside the source document.
- Reference implementation to generalize from: Black Mirror's
  `cmd/blackmirror/timebooking_review.go` + `timebooking_review.html`
  (event-sourced review server, atomic writes, replay) and the 2026-07-02
  spec-review artifact (static mode, localStorage + export). Marginalia is those
  two, made document-generic and lifted out of Black Mirror.

## Non-negotiable invariants

These are the design's failure modes — the PRD calls each one out explicitly:

- **Never mutate the source document.** Review is read-only against the input file.
- **Feedback events are append-only.** Never rewrite or delete lines in
  `<doc>.feedback.jsonl`. Later events on the same block override earlier ones
  only when *materializing* a resolution view; the log itself is immutable and
  replayed chronologically per block. `feedback.Materialize` is that view —
  events + current block hashes → per-block state, `stale` (the block was edited
  after the note) and `orphaned` (the block is gone). It never mutates an event:
  staleness is a reading of the log, not a field in it. Served at
  `GET /api/resolution` and embedded in the page payload.
- **No export step in the local agent loop.** In server mode every comment is on
  disk the instant it's saved (`POST /api/feedback` → append). A flow that makes
  the human copy/download/paste is a failure, tolerated only in static share mode.
- **Never depend on the Clipboard API.** Hosted/embedded contexts block it. Any
  copy affordance must degrade to a pre-selected textarea + manual Cmd+C;
  downloads wrapped in try/catch. Server mode avoids the class entirely.
- **Self-contained HTML in both modes** (inline CSS/JS). Static mode must survive
  strict CSP — no external requests, no webfonts (system fonts only).
- **Server mode is the product**; static `export`/`import` exists only for sharing
  with people off your machine/mesh. Don't build a second UI for phone/remote —
  the server binds on the Tailscale interface and the phone opens the same URL.

## Block anchoring & event schema (PRD §5.2–5.3)

Each block carries `{ block: "5.3/2" (section path + ordinal), quote: first ~90
chars, hash: sha256(normalized text)[:12] }`. A markdown list item extends its
list's ID with its position (`5.3/2.1`, nested `5.3/2.1.3`) and is an **inline**
block: its markup is the `<li>` inside the list's own HTML — annotated with the
anchor attributes by `internal/document/list.go` — so bullets, numbering and
nesting stay exactly as the document wrote them and the page renders no separate
element for it (`Block.Inline`). The list itself keeps its old ID, so notes
about the shape of a list, and feedback written before item anchoring, still
anchor. Tree documents keep the same
schema and only derive `block` differently — the node's own path
(`CreateOrderRequest/customer_id`, nested types dotted, members after a slash).
A mermaid statement is anchored by what it connects, with the link style and
any label left out (`worker -.retry.-> queue` → `worker-->queue`, chains
joined `a-->b-->c`, subgraph members after a slash). In a markdown fence those
paths extend the fence's own ID (`1/3/client-->api`) and the statements are
**inline** blocks, like list items: the `<pre>` is the source the author wrote,
annotated with anchors in place, and the fence keeps its old ID.
`block` re-locates cheaply, `quote`
makes events self-describing, `hash` flags a comment as **stale** on re-render
instead of silently misanchoring. Feedback event types: `comment`,
`suggest_edit` (text = replacement), `question`, `approve`, `reject`. A **Done**
button appends a `review_done` event — the agent's signal to proceed.

## Agent feedback loop (PRD §8)

The tool improves itself: on an unsupported format or unknown flag, the binary's
**error message advertises the `request-feature` skill** (with tool version).
Agents capture freely (file `agent-feedback`-labeled GitHub issues, dedupe
first); Berkay gates what becomes work via an `approved-for-agent` label; an
agent session implements the approved queue on branches/PRs. Guardrail: quote
document *structure*, never *content* — no secrets or private text in issues.

The third stage is `skills/implement-approved/SKILL.md` (`/marginalia:implement-approved`),
gated by `scripts/approved-queue.sh`: it reads `gh issue list --json` output and
names the oldest issue that states both an expected behaviour and an acceptance
criterion (exit 3 when the queue holds nothing implementable, so a run stops and
asks rather than guessing what "done" means). Rules the pipeline exists to keep:
**one issue → one PR based on `main` → stop** (never a second open PR, never a
stack — that is how work gets merged into a dead branch), never merge or approve,
match CI's pinned `golangci-lint` version (a linter built for an older Go
toolchain exits with a version error that reads like success), and after a merge
verify the files are on `main` and the issue actually closed rather than trusting
the PR's state. `.github/workflows/implement-approved.yml` runs it on
`workflow_dispatch` only; the cron is commented out because arming it grants an
agent write access on a timer.

## Build order (PRD §9)

M1 server mode for markdown (`serve` + JSONL + `review_done` + skill doc +
feedback scaffold) → **M2 revision loop (hash-stale, resolution view) — done** →
**M3 trees: `.proto`, JSON/YAML/TOML, mermaid — done** → **M4 automated implementation
pipeline — in place (skill + triage gate + manual workflow; schedule disarmed)**
→ M5 static share mode.

## Commands

- Build: `go build ./...`  ·  Run: `go run . serve <doc.md> --open`
- Suggestions: `go run . suggestions <doc.md> [--json]`
- Test: `go test -race ./...`  ·  single: `go test -run TestName ./internal/document/`
- Coverage gate: `go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80`
- Lint: `golangci-lint run`
- Skills live in `skills/`; the repo is its own Claude plugin marketplace (`.claude-plugin/`).
