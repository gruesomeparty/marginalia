# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status: M1–M5 shipped

The repo has a working Go module implementing PRD milestone **M1** — server mode
for markdown (`serve`, block anchoring, append-only JSONL feedback,
`review_done`) — plus full CI/CD and the installable Claude plugin (skills +
`/marginalia:review-doc`). **M3** is in: `.proto` schemas (issue #15) and
JSON/YAML/TOML trees (issue #2) render as folding trees anchored by node path,
and mermaid flowcharts anchor per statement (issue #25) — in a markdown fence
and as `.mmd`/`.mermaid` documents — and are **drawn** as anchored SVG when
mermaid-cli is installed (issue #36): the shape you click in the picture is the
statement your note lands on, source one toggle away.
Reviews are configurable (issue #3): `serve --config review.yaml` frames the
review and defines the vocabulary the reviewer answers in, enforced server-side,
and carries the requester's per-block notes and the parts it is not asking about
(issue #18). Presentation is themed (`--theme`, Catppuccin included), code is
highlighted server-side, and the reviewer owns light/dark and ligatures.
Share mode (issue #5) is in: `export` writes a one-file review page that loads
nothing and saves into the reviewer's browser, `import` merges what they send
back onto the canonical log, append-only and idempotent.
Multi-document sessions (issue #8) serve a set — a directory, several paths, or
a `.marginalia.yml`-curated list — one page per document, and markdown list
items anchor individually (issue #12). **M2** is in: `feedback.Materialize`
replays a log against the document as it now reads, so a re-render marks notes
stale, surfaces ones whose block is gone, and shows the current state per block
(issue #1).
`PRD.md` is the authoritative spec; read it before writing anything. This file summarizes it and flags the constraints that are
easy to violate. When the PRD and this file disagree, the PRD wins (and update
this file). Every milestone in the build order below has shipped; what is open
is in the tracker, not in the roadmap.

## What Marginalia is

A document-review tool an agent hands to a human. It renders a markdown (later
JSON/YAML) file as a readable page, collects inline comments anchored to blocks,
and writes them back as structured, append-only feedback events the agent
consumes directly. Three parts: **render, annotate, export events.** Its primary
users are agents, not humans.

## Planned architecture (from PRD §6)

- **Single Go binary**, cobra subcommands: `serve`, `suggestions`, `export`,
  `import`, `reply`, `addressed`, `review`, `mcp`, `version`. `suggestions` reads a document and its log from disk (no
  server) and splits `suggest_edit` events into applicable and
  needs-confirmation via `feedback.Resolution.Suggestions()`. Applying requires
  all three of: the suggestion is the note that stands for its block, its hash
  matches the block now, and it has a hash at all — `Materialize` treats a
  hash-less note as not-stale, which is right for reading and not good enough
  for editing. The tool never applies anything: *never mutate the source
  document*.
- `internal/mcpserver` is the agent-facing surface (`marginalia mcp`, stdio):
  `review_document`, `feedback_since`, `review_status`, `reply_to_note`,
  `mark_addressed`, `await_review_done`, `close_review`. Three rules hold it together. **Stdout belongs to the
  protocol** — that is why `server.Options.Log` exists and why every notice
  goes through `s.logf`; one stray `fmt.Println` corrupts a whole session and
  looks like a broken client. **Reads are disk-backed**, never served from the
  running HTTP server, so they survive the agent's session ending and let any
  number of agents follow one review; the wait polls the log's size+mtime, not
  `/api/revision`, which counts document re-parses and moves for a different
  reason. **An agent may answer and may claim, never decide**: `reply_to_note`
  and `mark_addressed` are the only tools that write, each writes one fixed
  type, and both name the note they are about. There is deliberately **no**
  confirm or reopen tool — that is the reviewer's verdict on the agent's work,
  and an agent that could settle its own note would make the revision loop
  decorative. The rest of the log is the human's answers, and `review_done` is
  the signal the handover exists to produce — an agent that could append one
  could answer its own question. `--root` confines
  every path a tool names (relative paths resolve against it), because a tool
  argument can come from text the model read and `.yaml` is a supported input.
- `internal/session` is the pipeline both callers share — `Build` turns paths
  plus options into a `*server.Server`, and `Advertise`/`RouteSetError` carry
  the request-feature wording. It lives under `internal/` because `cmd` cannot
  be imported; `cmd` keeps the flags and sets `session.Version` from its own
  `version`, which is what the release ldflags write to.
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
  they occupy so a fence can be annotated in place. `internal/diagram` draws the
  diagram: it shells out to mermaid's own CLI (`mmdc`), sanitizes the SVG
  (scripts, handlers, external refs, `@import` — the page runs no code it did
  not write), stamps the page's block ids onto the shapes as `data-anchor`, and
  caches by source hash in the user cache dir, never beside the document. That
  reverses the earlier "no picture" decision *on the rendering route only*: an
  inlined SVG needs no script and makes no request, so share mode and strict
  CSP still hold, and the single Go binary still works with no Node installed —
  an unavailable renderer means the page shows the anchored source, as it
  always did. The reviewer's theme colours the drawing, and the way it
  does is the point (`palette.go`): mermaid inlines its own stylesheet keyed on
  the svg's id, which outranks anything the page can write, so instead of
  shouting over it with `!important` the SVG is rewritten on the way out —
  every colour it chose becomes `var(--mg-node-fill)`, `var(--mg-line)`,
  `var(--mg-text)` and friends, which the page defines from the active theme.
  A custom property also inherits and has no specificity, so hover and
  "has notes" recolour a shape by handing it a different value, not by
  out-ranking anything; only `stroke-width` is still taken by force. A rule
  whose selector `roleOf` does not recognise keeps mermaid's own colours — a
  picture in the wrong palette beats one we broke recolouring. Changing that
  post-processing means bumping the cache key version in `hash`. Mermaid names its
  own parts (`L_<from>_<to>_<n>` on edge paths and their labels,
  `…-flowchart-<id>-<n>` on nodes, the svg id plus the name on clusters), which
  is what makes a picture anchorable; an identifier containing `_` makes the
  split ambiguous, so every split is tried and the one naming a real statement
  wins. Each anchored edge also carries an invisible wide copy of itself
  (`.mg-hit`) so a two-pixel arrow is a clickable target, and a box no
  statement declares answers for the statement that named it. Because those
  names are mermaid's internals and not API, `internal/diagram/testdata` holds
  a **real** `mmdc` output for `flowchart.mmd`, its version in the filename,
  and `fixture_test.go` drives it through the whole pipeline: a mermaid
  upgrade that renames a shape fails a test naming it, instead of leaving a
  picture that draws, looks right, and silently cannot be clicked. Adopting a
  new version means re-rendering that one file and extending the matcher.
- `serve` and `export` take `--diagrams=auto|off` and `--mmdc <path>`;
  `MARGINALIA_MMDC` and `MARGINALIA_MMDC_ARGS` name the binary and extra
  arguments (a puppeteer config, say) without a flag. `export` *fails* when a
  diagram cannot be drawn — the file is about to be handed to someone who
  cannot re-run the command — while `serve` says so once and serves the source.
  `rescan` re-draws after a watch re-parse, because a re-parse throws the SVG
  away with the old blocks.
- A requester note carries the document it is about (`review.Note.Doc`), and
  `web.ReviewInfo` filters notes to the document being rendered: block ids are
  per-document, so `1/2` exists in every markdown file of a set and an
  unscoped note would be rendered beside the wrong block and reported as
  unanchored on every other page. A note naming a document the set does not
  serve is named at startup rather than dropped.
- `internal/review/presets/*.yaml` are framings embedded in the binary
  (`--review adr|copy|schema|security`), so an agent picks a vocabulary rather
  than inventing one and the events compare across runs. `Config.Overlay`
  layers a file over a preset: yaml.v3 decoding into a populated struct leaves
  absent keys alone, which is exactly the layering wanted — naming `actions:`
  replaces the vocabulary, naming only `instructions:` reframes it. `Load` is
  now `Default()` + `Overlay`, so there is one decoder configuration
  (`KnownFields(true)`) wherever a config comes from.
- `internal/review` is the review configuration: framing, the action
  vocabulary (built-ins plus configured ones), structured fields, read-only
  patterns. It is the **only** authority on which event types exist — there is
  deliberately no `feedback.ValidType` any more, because a second list would
  drift. `Config.Validate` and `Config.Locked` run on the server, not just in
  the page: rendering a rule is not enforcing it. `review_done` and `reply` are
  **protocol**, not vocabulary (`Protocol`, `IsProtocol`, `ValidateProtocol`): a
  config cannot define them, `Validate` refuses them, and `builtins: false`
  cannot turn a reply off. A reply also bypasses `Locked` — a readonly pattern
  added after a thread opened must not strand it.
- Threaded replies (issue #47): an event may carry `reply_to`, naming another
  event by `feedback.NoteID` — sha256 of the same tuple `cmd/import.go` already
  treats as an event's identity, **derived and never stored**, so every log on
  disk has ids the day it ships and an id survives export/import, which rewrites
  `Doc`. `Materialize` hangs each reply under the note it answers as a
  `feedback.Reply` (deliberately not a `Note`: a type that could nest would
  invite a forum, and the MCP SDK cannot build a schema for a recursive one
  either), re-rooting a reply to a reply with a depth-bounded walk, and
  surfacing one that names nothing as a `Dangling` note of its own rather than
  dropping it. `Resolution.Replies` counts them **separately from `Comments`**:
  the header count is how much the reviewer said and must not inflate because
  the agent answered. The server's `check()` gates a reply on the id resolving
  in *that document's* log (`Server.knows`), `POST /api/feedback` echoes the
  saved event wearing its `id` (the page has no sha256 it can rely on in every
  context it runs in), `marginalia reply` and the `reply_to_note` MCP tool are
  the agent's side, and `import` refuses a reply whose target is in neither the
  incoming file nor the existing log. `reply_to_note` is the **only** MCP tool
  that writes, and it writes one type: an agent that could append a `comment` or
  a `review_done` could answer its own question.
- The revision loop (issue #48) is three more protocol types — `addressed`,
  `confirm`, `reopen` — all carrying `reply_to`, so they travel the same road
  as a reply and are split apart when attached: an answer goes to
  `Note.Replies`, a status to `Note.Progress`. `Note.Status` is the latest
  progress event's, defaulting to `outstanding`, and is **orthogonal to
  `Stale`**: stale says the *text* moved, status says whether anyone claimed to
  act on the note. Conflating them is the bug #48 exists to fix — before it, a
  block edited in answer to a note and one edited for unrelated reasons looked
  identical, so a second round meant re-reading the whole document. An
  `addressed` records the hash the block had when the note was written (taken
  from the note itself, so the check costs the agent nothing and cannot be
  fudged by forgetting); if the thing the note is about *still* reads the same,
  nothing changed and the note is marked `Unclaimed` — reported, never
  suppressed, because the edit may be elsewhere. **The claim is checked against
  whatever the note is about**: the block's hash for a block-level note, and
  the *sentence* for a sub-anchored one (issue #67 — checking the block meant
  editing any other sentence in the paragraph made an unrelated claim read as
  substantiated). That also makes the two readings symmetric: a sub-anchored
  note is stale when its sentence is gone, and its claim is unsubstantiated
  when its sentence is still there. A hash-less claim is not marked, the same "cannot be
  proven is not false" rule `Suggestions()` applies. `Resolution.Outstanding`
  excludes `approve` (it asks for nothing) so a fully-approved document does
  not read as a full queue. `checkVerdicts` counts only non-protocol events, so
  an agent's reply or claim cannot satisfy `require_verdict` on the reviewer's
  behalf. `marginalia addressed` and `mark_addressed` are the agent's side;
  confirm and reopen exist only in the page, deliberately.
- The long-document flow (issue #46) is four things in the page, all built on
  the token layer. **Progress is answered-of-commentable**, where commentable
  is `!b.readonly` and `readonly` in the payload *is* `cfg.Locked()` — the
  server's own gate, not a second guess; if they diverged the bar would say
  finished and Done would refuse, so `render_test.go` asserts the projection
  equals `Locked` for every block. **Movement** binds `ArrowDown`/`ArrowUp`
  (and shifted, for next/previous *unanswered*, wrapping) plus `j`/`k`/`n`/`p`
  when free: a configured action key always wins, because the agent asked for
  that vocabulary, and since `review` requires a key to be a single character,
  the arrows can never be taken — without them a review configuring `nit` with
  key `n` would silently lose the queue shortcut. **Filters** resolve in the
  same pass as folding (`applyFolds`), because two places setting `hidden`
  fight; an inline block dims rather than hides, since removing a list item
  renumbers the list around it, and the menu label always says how many blocks
  are out of view. **`require_verdict`** now names the outstanding blocks and
  scrolls to one on click, computed in the page but still enforced by the
  server — a 409 re-opens the same panel, so if the two ever disagree the
  server wins visibly.
- Sentence-level notes (issue #49) are a **new event field, never a longer
  block id**: a sub-block id like `5.3/2#1` would be accepted by every consumer
  that already reads `block` and would silently mean something different to
  each. `Event.Sub` carries quote + prefix + suffix + start + hash — the W3C
  annotation model's selectors, for the same reason it chose them: an offset
  alone always "finds" something, and a note that silently re-anchors to a
  neighbouring sentence is worse than one that admits it is stale.
  `feedback.Locate` scores candidates by surviving context and breaks ties by
  distance from the recorded offset. **A sub-anchored note's staleness is
  decided by its own sentence, not by the block hash** — judging it by the hash
  would make the feature worthless on any paragraph anyone edits — and
  `Note.At` is where it landed, so the page marks it without searching again.
  The page sends *only* the quote; `Server.anchorSub` rebuilds the anchor
  against the server's own `PlainText` and refuses a quote the block does not
  contain, so page and server cannot disagree about what was selected, and
  `POST /api/feedback` returns `at` for the same reason it returns `id`. The
  offer is a button on a selection, never a hijacked mouseup, and the block's
  click handler ignores a click that ends a drag-select — otherwise "comment on
  a sentence" would have cost "select a sentence". Scope is markdown prose:
  trees and fences are skipped because their rendered text and `PlainText` can
  differ.
- `feedback.Materialize` takes `[]feedback.Block{ID, Hash, Text}` rather than a
  `hashes` map plus an `order` slice. The pair could express a block that was
  known but unlisted, five callers each built it with the same loop, and a
  sub-anchor needs the text anyway.
- Unified diffs (issue #52, first of its formats) are parsed by
  `internal/unidiff` — tolerant and structural like `protoschema` and
  `mermaid` — and wired in by `document/diff.go` as a two-level tree: a file
  per top-level node, its hunks beneath. A hunk is anchored by **ordinal, not
  line number** (`handlers.go/2`): regenerating a patch after an earlier hunk
  changes shifts every line number below it, which would orphan every note
  under it, whereas an ordinal only moves when a hunk is inserted before and
  the hash catches that. Line markers are hashed with the text, since `+x` and
  `-x` are opposite statements. A *file* block hashes its identity and tally
  and not its hunks, so a note about the file survives its hunks being edited.
  Because file ids are real paths and `review`'s patterns already treat `/` as
  a separator, `readonly: ["internal/"]` mutes a directory for free. The
  `git format-patch` preamble becomes an `@message` block — the commit message
  is part of what is signed off — and an empty patch gets an `@empty` block
  rather than rendering a blank page.
- `internal/highlight` tokenizes code at render time — fenced blocks and
  `.proto` declarations — because a client-side highlighter means a CDN script
  and the page must survive strict CSP. Small on purpose: comments, strings,
  numbers, keywords, a handful of languages, passthrough for the rest.
- The page's CSS is a **design language in two layers**, and the split is the
  rule: `:root` defines palette-invariant tokens — type scale (`--fs-*`),
  spacing (`--sp-*`), radii (`--r-*`), elevation (`--e-*`), focus ring, accent
  tints — and a palette may define **colour tokens only**. A palette that has
  to redefine a radius is a bug in the component. `internal/web/tokens_test.go`
  enforces it mechanically, because the way this decayed the first time was one
  reasonable-looking rule at a time: it fails on a raw hex in a rule, a font
  size off the scale, a spelled-out font stack, a variable read but never
  defined, and a palette that omits a colour the others define. That last one
  is not hypothetical — `--marker` was defined for light and never redefined
  for dark, so every dark-mode badge wore the light palette's yellow.
- Themes are CSS variable sets on `[data-palette]` and light/dark a mode on
  `[data-mode]`, so `--theme` and the reviewer's own toggle are one mechanism.
  The reviewer's display choices (mode, ligatures) live in `localStorage`,
  applied by a small head script before first paint; every access is wrapped,
  because storage throws in a private window.
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
annotated with anchors in place, and the fence keeps its old ID. The drawn SVG
carries the same ids as `data-anchor` on its shapes, so picture and source are
one surface and a note from either is the same event.
`block` re-locates cheaply, `quote`
makes events self-describing, `hash` flags a comment as **stale** on re-render
instead of silently misanchoring. A note about one sentence adds `sub`
(quote + context + offset) *beside* that anchor and never inside `block`. Feedback event types: `comment`,
`suggest_edit` (text = replacement), `question`, `approve`, `reject`, `reply`
(an answer to another note, carrying `reply_to`), `addressed`/`confirm`/`reopen`
(the revision loop, also carrying `reply_to`), plus any
action the requesting agent configured (`blocker`, `nit`, …) — an action with
nothing to fill in is one tap, and one that declares `fields` carries them in
the event's `fields` map. A **Done**
button appends a `review_done` event — the agent's signal to proceed;
`require_verdict` withholds it until every commentable block is answered.

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
→ **M5 static share mode (`export`/`import`) — done**.

## Commands

- Build: `go build ./...`  ·  Run: `go run . serve <doc.md> --open`
- Drawn diagrams (optional): `npm i -g @mermaid-js/mermaid-cli`. As root — any
  container, CI included — puppeteer needs `{"args":["--no-sandbox",
  "--disable-setuid-sandbox","--disable-dev-shm-usage"]}` in a file named by
  `MARGINALIA_MMDC_ARGS="-p that.json"`, and `PUPPETEER_EXECUTABLE_PATH` if a
  Chromium is already on the box. Startup says which mode you got; README and
  `skills/review-doc` carry the same recipe for agents.
- Suggestions: `go run . suggestions <doc.md> [--json]`
- Test: `go test -race ./...`  ·  single: `go test -run TestName ./internal/document/`
- Coverage gate: `go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80`
- Lint: `golangci-lint run`
- Skills live in `skills/`; the repo is its own Claude plugin marketplace (`.claude-plugin/`).
