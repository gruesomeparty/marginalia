# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status: M1 shipped; M3 in progress

The repo has a working Go module implementing PRD milestone **M1** — server mode
for markdown (`serve`, block anchoring, append-only JSONL feedback,
`review_done`) — plus full CI/CD and the installable Claude plugin (skills +
`/marginalia:review-doc`). **M3** is in: `.proto` schemas (issue #15) and
JSON/YAML/TOML trees (issue #2) render as folding trees anchored by node path.
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

- **Single Go binary**, cobra subcommands: `serve`, `export`, `import`, `version`.
- HTML template embedded via `go:embed`; one template feeds both server and static modes.
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
  `MetaData.Keys()`.
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
  replayed chronologically per block.
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
chars, hash: sha256(normalized text)[:12] }`. Tree documents keep the same
schema and only derive `block` differently — the node's own path
(`CreateOrderRequest/customer_id`, nested types dotted, members after a slash). `block` re-locates cheaply, `quote`
makes events self-describing, `hash` flags a comment as **stale** on re-render
instead of silently misanchoring. Feedback event types: `comment`,
`suggest_edit` (text = replacement), `question`, `approve`, `reject`. A **Done**
button appends a `review_done` event — the agent's signal to proceed.

## Agent feedback loop (PRD §8)

The tool improves itself: on an unsupported format or unknown flag, the binary's
**error message advertises the `request-feature` skill** (with tool version).
Agents capture freely (file `agent-feedback`-labeled GitHub issues, dedupe
first); Berkay gates what becomes work via an `approved-for-agent` label; a
scheduled agent implements the approved queue on branches/PRs. Guardrail: quote
document *structure*, never *content* — no secrets or private text in issues.

## Build order (PRD §9)

M1 server mode for markdown (`serve` + JSONL + `review_done` + skill doc +
feedback scaffold) → M2 revision loop (hash-stale, resolution view) → **M3
trees: `.proto`, JSON/YAML/TOML — done** → M4 automated implementation pipeline
→ M5 static share mode.

## Commands

- Build: `go build ./...`  ·  Run: `go run . serve <doc.md> --open`
- Test: `go test -race ./...`  ·  single: `go test -run TestName ./internal/document/`
- Coverage gate: `go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80`
- Lint: `golangci-lint run`
- Skills live in `skills/`; the repo is its own Claude plugin marketplace (`.claude-plugin/`).
