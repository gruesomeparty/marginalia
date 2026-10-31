# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status: M1–M6 shipped

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
**M6, hosted mode** (epic #91, PRD §5.9), is in: one always-on
instance (`marginalia host`) that agents on other machines publish *content*
to over remote MCP (`mcp --http`, Streamable HTTP, a claude.ai connector), the
owner reviews from an inbox, and whose state lives behind `internal/storage` —
immutable blobs plus append-only logs, disk the default backend, R2 the first
remote one — on a single Cloudflare Container behind Cloudflare Access, which
`deploy/cloudflare` deploys. It is additive: `serve` and stdio `mcp` are
untouched and stay the default product.
`PRD.md` is the authoritative spec; read it before writing anything. This file summarizes it and flags the constraints that are
easy to violate. When the PRD and this file disagree, the PRD wins (and update
this file). Every milestone in the build order below has shipped; what is
open is in the tracker.

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
  reason. The wait is short by default (45s, issue #182 — MCP clients such as
  claude.ai's connector drop a tool call at 60s) and is called in a loop with
  the returned cursor; it also returns `reason: "question"` on an unanswered
  `question` past the cursor, so the agent can answer before Done. **An agent may answer and may claim, never decide**: `reply_to_note`
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
- `serve --until-done` / `--timeout` and `GET /api/wait` (issue #44,
  `internal/server/wait.go`): `Server.append` calls `recorded`, which bumps a
  counter, wakes long-polls and closes `Done()` once every served document has
  a `review_done` **written by this process** — one already in a log is an
  earlier round's answer. `serveUntil` (cmd) cancels Run on Done or the
  deadline, so the drain still finishes before the exit; a timeout exits 124
  through `exitError`, which `Execute` honours.
- Server routes: `GET /` (first document), `GET /d/{rel...}` (one page per
  document), `GET /api/doc[?doc=]`, `GET|POST /api/feedback[?doc=]`,
  `POST /api/session_done`. A posted event names its document and the server
  only writes to documents in the served set — a stale page must not be able to
  append elsewhere. Per-document `review_done` comes from the page's Done
  button; `session_done` writes one to every log with `text: "session"`.
- `serve` has an opt-in token (issue #54, `internal/server/token.go`):
  `MARGINALIA_SERVE_TOKEN`, env only, makes `Handler()` refuse any request
  without it (bearer header, or the HttpOnly, SameSite=Lax cookie (a digest of the token, named per port) a one-time `?token=`
  sets before redirecting the token out of the URL; a refused page gets a
  one-field form), compared by digest in constant time, and pins every
  event's author to `--author` through `Options.AuthorOf`. No variable, no
  change. `export` writes the source file's sha256 into the page, the
  share JSON carries it back as `source`, and `import` refuses a reply
  whose `doc` names another file (by base name, so `--doc` can follow a
  move) and warns, then merges, when the hash differs.
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
  they occupy so a fence can be annotated in place. Flowcharts, and since
  issue #153 sequence, state and ER diagrams (`kinds.go`): a message is
  `from->to` (repeats numbered by the id assigner, so pair + ordinal), a
  transition `from-->to`, a relationship `A--B`; groups, composite states and
  entities are containers. Only a flowchart is split at `;`. Their pictures
  are stamped by `internal/diagram/kinds.go` (issue #153), from what mermaid
  12 names in them: a participant, state or entity by name; a message,
  note, group, transition or relationship by its ordinal — and only when the
  drawing holds exactly as many as the source, since a miscount means the
  pairing would guess (groups in the order they close, since mermaid draws a
  loop at its `end`, inner first). Transitions go unstamped once a composite state is
  involved (mermaid numbers those edges its own way), and ER attributes stay
  source-only. A message line gets a wide invisible `line.mg-hit`. `internal/diagram` draws the
  diagram: it shells out to mermaid's own CLI (`mmdc`), sanitizes the SVG
  (scripts, handlers, external refs, `@import`, and the `@font-face` webfonts
  mermaid-cli 12 embeds, issue #87 — the page runs no code it did
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
  statement declares answers for the statement that named it. The keyboard
  reaches the picture too (issue #154): the page gives each statement one
  focusable shape (its box, or an arrow's label), named "Statement …", in the
  order the *source* declares them rather than mermaid's paint order, as a
  roving tab stop — Tab and the arrows move, Enter opens the composer, and
  closing it hands focus back. That is page script, so the SVG and its cache
  are unchanged. Because those
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
- A requester note is answerable (issue #90): `Note.ID` is `req:<id>` from
  the config or `req:` + a hash of doc/block/text (text, not position, so
  inserting a note never hands its neighbour's answers to it). The page's
  Answer button posts a `reply` with that `reply_to`; `Server.knows` accepts a
  requester id addressed to the document, for `reply` only. Materialize turns
  such a reply into a root note with `Answers` set instead of `Dangling`, so it
  is outstanding work for the agent, counted in `Replies` and not `Comments`;
  the page shows it under the sidenote and not in the block's notes. `import`
  lets a `req:` reply through, since no log holds the question.
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
  equals `Locked` for every block. The **outline** (issue #132,
  `web/sections.go`) only regroups that rule: each block carries the
  `section` it rolls up into (the outermost heading level that occurs at
  least twice for markdown; a tree's top-level node, one below a lone root),
  and `sections_test.go` pins that the per-section totals sum to the
  document's. **Movement** binds `ArrowDown`/`ArrowUp`
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
  server wins visibly. The **all-notes panel** (issue #130, header
  "Notes") lists every note in document order from `byBlock`, through the
  same `passes()` filter as the page — one filter, not two — then the notes
  that no longer anchor; an entry lands on its block. Open/closed is a
  `localStorage` display choice. The **margin rail** (issue #130 part 2) is page
  script only: `layoutRail()` (called from `count()`, on resize and by
  ResizeObservers on `main` and each placed box) moves every `.existing`
  box, found through `box.mgFor` set in `attach()`, into an absolutely
  positioned column right of the text, top-aligned to its block and pushed
  below the previous box on overlap, whenever ≥1200px leaves 18rem beside
  `main`; otherwise boxes stay inline exactly as before. The composer and
  requester sidenotes always stay inline. On a phone (≤640px, issue #131) the header is
  one row (title, progress as a fraction, the menus by their short labels,
  Done), the note-count badge moves into the narrower gutter, and the
  composer is a bottom sheet: it quotes its block, rides above the on-screen
  keyboard through `visualViewport`, and a tap outside closes it unless
  something is typed. That is a media query plus `phoneSheet()`; desktop is
  untouched.
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
- SQL (issue #152) is split by `internal/sqlstmt` — a scanner, not a
  grammar, returning byte ranges like `internal/mermaid` — that ends a
  statement at a semicolon outside strings (`E''` honours backslashes, plain
  `''` does not), quoted identifiers, nested comments, dollar quotes and
  BEGIN/CASE … END bodies (`BEGIN;` as a statement's first word is a
  transaction). A `.sql` file is a flat tree with ids `<ordinal>:<kind>[:<object>]`
  (`3:alter-table:orders`); ordinal first on purpose, so an inserted
  statement orphans a note rather than inheriting it. A `sql` fence of two
  or more statements anchors them inline under the fence's id the way a
  mermaid fence does (`inlineAnchor` is shared); a one-statement fence stays
  one highlighted block.
- HCL (issue #151: `.tf`, `.tfvars`, `.hcl`) is read by `internal/hcl`, a
  hand-written structural reader rather than hashicorp's `hclsyntax`, which
  refuses a file with a syntax error and would be a heavy dependency. It
  skips expressions through brackets, strings, `${…}` templates and
  heredocs, never fails (an unreadable line is an `unknown` node), and is
  fuzzed. Ids are the path the file already spells — type and labels dotted
  (`resource.aws_s3_bucket.logs`), body members after a slash, repeats
  numbered by `treeBuilder` — and a block hashes its header only, as a proto
  message does.
- OpenAPI (issue #150, `document/openapi.go`) is not a format: a JSON/YAML/TOML
  document whose one root has an `openapi` or `swagger` key is anchored by
  what the spec names — `GET /orders/{id}`, the path item `/orders/{id}`,
  `components/schemas/Order`, Swagger's `definitions/Order` — and generic
  data paths below and around those. Detection reads the decoded document,
  never the file name; a misshapen `paths` or `components` falls back to
  the generic tree under it. Notes written on a spec before this used the
  generic paths and now surface as orphaned.
- `internal/highlight` tokenizes code at render time — fenced blocks and
  `.proto` declarations — because a client-side highlighter means a CDN script
  and the page must survive strict CSP. Small on purpose: comments, strings,
  numbers, keywords, a handful of languages, passthrough for the rest.
- The page's CSS is a **design language in two layers**, and the split is the
  rule: `internal/web/tokens.css` defines palette-invariant tokens — type
  scale (`--fs-*`), spacing (`--sp-*`), radii (`--r-*`), elevation (`--e-*`),
  focus ring, accent tints — and the palettes, which may define **colour
  tokens only**. A palette that has to redefine a radius is a bug in the
  component. Every page template (`review.html.tmpl`, `inbox.html.tmpl`)
  includes it with `{{template "tokens.css"}}` and then speaks only in tokens;
  `internal/web/tokens_test.go` enforces it mechanically over every page,
  because the way this decayed the first time was one reasonable-looking rule
  at a time: it fails on a page with tokens of its own, a raw hex in a rule, a
  font size off the scale, a spelled-out font stack, a variable read but never
  defined, and a palette that omits a colour the others define. That last one
  is not hypothetical — `--marker` was defined for light and never redefined
  for dark, so every dark-mode badge wore the light palette's yellow.
  `contrast_test.go` holds every palette in both modes to WCAG AA over the
  pairs the pages actually draw (issue #156): 4.5:1 text, 3:1 for a line that
  carries meaning, which is why `--marker` (a fill) has a `--marker-ink` for
  strokes and underlines. A rule that puts a colour on a new background adds
  its pair to that table.
- The **inbox** (`web.RenderInbox`, `hosted/inbox.go`) is the hosted front
  page: one row per review from `Host.rows()`, built from the parsed revision
  and the in-memory log (zero store reads after startup, pinned by test),
  status from the log — `awaiting` (nothing said), `in_progress` (human notes,
  no `review_done`), `done`, `replied` (a `reply`/`addressed` after the last
  `review_done`) — and counts from `feedback.Materialize`, never a second
  tally. Tabs are a query string; `/api/reviews` is the same rows as JSON.
  A row leads with its status and outstanding count, metadata after
  (issue #134), and rows group under Today / Earlier by last activity in UTC
  — headings only when both groups hold a row (`Inbox.Now` pins the clock in
  tests). Grouping is presentation only; `Host.rows()` is unchanged.
  Filters and grouping live in the query string (`web.InboxFilter`: `q`
  searches title, agent, framing and document names; `framing`, `agent`;
  `group=day|status|framing|agent|none`), so a filtered inbox is a link and
  the form works with no script (a few lines only submit it when a menu
  changes). Tab counts are taken after the filters, `/api/reviews` accepts
  the same parameters, and a row's `framing` is its preset, `custom` for an
  inline config alone, or empty for the default.
- Themes are CSS variable sets on `[data-palette]` and light/dark a mode on
  `[data-mode]`, so `--theme` and the reviewer's own toggle are one mechanism.
  The reviewer's display choices (mode, ligatures) live in `localStorage`,
  applied by a small head script before first paint; every access is wrapped,
  because storage throws in a private window.
- **No database.** Documents in, HTML out, JSONL beside the source document.
- **Persistent state goes through `internal/storage`** (PRD §5.9, §6): two
  primitives — immutable blobs (`PutBlob` is put-if-absent, `GetBlob`, `List`
  by prefix) and append-only logs (`AppendLog`, `ReadLog`, `LogToken`) — plus
  `Move` of a prefix for archiving. The interface has no rewrite, truncate or
  delete for log lines, on purpose: the append-only invariant is structural,
  not a convention, and `internal/storage/boundary_test.go` fails on an
  `O_APPEND` or `os.WriteFile` anywhere else. `storage.Disk` is the default
  backend — `feedback.NewStore(doc)` is a Disk rooted at the document's
  directory with the key `<doc>.feedback.jsonl`, so the file is byte-identical
  to before (pinned by `TestLogBytesArePinned` and
  `TestServeWritesTheSameLogBytes`) — and `feedback.OpenStore(backend, key)`
  is the same log on any backend, which is what hosted mode uses.
  `storage.Memory` is the reference backend tests of `host` run on.
  `storage.R2` (`r2.go`, minio-go over R2's S3 API, `r2://bucket/prefix`,
  credentials from `MARGINALIA_R2_*` only) stores one object per event under
  `<log>/<20-digit nanos>-<random>.json`, written put-if-absent with
  `If-None-Match: *` (a 501 switches it to a HEAD-then-PUT fallback, reported
  by `ConditionalPut`), and `List` collapses those objects into the log's
  key so every backend shows one layout; its `LogToken` costs a listing,
  which is why the host answers waits from `WriteThrough` instead.
  `storage/s3test` is the in-process S3 fake (conditional PUT, paging, copy,
  a request counter) CI runs the suite and the host's cost tests on;
  `TestR2Integration` runs the same suite on a real bucket when the
  `MARGINALIA_R2_*` variables are set. The
  conformance suite (`storagetest.Run`) runs against every backend; a change
  token (`feedback.Store.Changed`) is what `await_review_done` polls, not the
  file. The source document `serve` reads, `--watch`'s polling and the
  diagram cache are not state and stay on `os`.
- **Hosted mode** (`marginalia host`, `internal/hosted`, PRD §5.9) is one
  listener for many reviews: `/` inbox, `/r/{id}/…` the per-review
  `server.Server` routes mounted with `http.StripPrefix` and told their mount
  through `server.Options.BasePath` → `web.Page.Base` → the payload's `base`,
  which every fetch and nav link in the page starts from (the page never
  assumes `/`), and told its inbox through `server.Options.InboxURL` (`/`),
  which gives the header an **← Inbox** link and the `i` key — neither exists
  under `serve` or `export`, which have no inbox, `/mcp` Streamable HTTP (`mcpserver.Transport`, shared with
  `mcp --http`), `/healthz`. Keys are the same on every backend:
  `reviews/<id>/meta/<n>.json` (versioned, highest wins),
  `reviews/<id>/rev/<n>/<name>` (immutable snapshots), `reviews/<id>/log/<name>`
  (the feedback log, `feedback.OpenStore`). Ids are 128 random bits, URL-safe;
  `hosted.CheckName` refuses a document name that is not one plain segment.
  Agents publish bytes plus a name (`publish_document`, `hosted/tools.go`;
  `review_document` is deliberately not registered there); a snapshot is
  immutable and a republish (`review_id` given → `Host.Republish`, revision
  n+1, framing inherited unless resent, the log keys unchanged) re-anchors
  every note through `feedback.Materialize`. The hosted tools are the stdio
  tools over `mcpserver.Target` — `EventsSince`, `StatusOf`, `WaitForDone`,
  `Answer` are exported for exactly that — so the two modes cannot drift in
  what they report, and they wait on the write-through token, never a file.
  `hosted.Open`
  lists the store once and rebuilds every review; the process then runs on
  `storage.WriteThrough` — logs in memory, appends to the backend first —
  so single instance, single writer, and nothing polls the store (every R2
  call is billed; `TestHostReadsTheStoreOnceAtStartup` pins zero reads per
  page and one write per comment). Identity is
  resolved per request before any handler (`internal/identity`,
  `hosted/identity.go`): a human is an edge JWT — `Cf-Access-Jwt-Assertion`
  or the `CF_Authorization` cookie — verified by `identity.Verifier` against
  the issuer's JWKS with go-jose (cached, refreshed on an unknown `kid`,
  rate-limited; asymmetric algorithms only), plus `iss`, `aud`, `exp`, `nbf`;
  a resolved human must also be on `Options.Reviewers` (`--reviewers`,
  `MARGINALIA_REVIEWERS`) when that list is set, else 403 — the edge
  deciding who may log in is not this host deciding who may review, and the
  list is where multiple reviewers start; `Identity` carries `Provider` and
  `Subject` beside `Name` so a later login provider has a stable id;
  an agent is the `/mcp` bearer (`mcpserver.StaticTokenVerifier` behind
  go-sdk's `RequireBearerToken`, whose `TokenInfo` reaches tool handlers as
  `req.Extra.TokenInfo`) or an OAuth access token (`hosted/oauth.go`:
  Marginalia is an RFC 9728 *resource server* only — never issues tokens;
  `/.well-known/oauth-protected-resource[/mcp]` names `--public-url`'s
  `/mcp` as the resource and `--mcp-oauth-issuer` as its server, an
  unauthenticated `/mcp` gets `WWW-Authenticate: Bearer resource_metadata=…`,
  and a JWT bearer is verified by the same `identity.Verifier` with `aud` =
  the `/mcp` URL; its subject becomes `agent:<sub>`, still an agent's seat;
  the static token keeps working beside it; `docs/hosting.md` is the
  operator's guide). Page routes run under
  `requireHuman` and refuse an agent credential outright (403); `/mcp` has
  no tool that writes anything but `reply` and `addressed`; the one other
  agent door is `GET /agent/r/{id}/wait` (issue #186, `hosted/agentwait.go`),
  a long-poll in `/api/wait`'s shape behind the `/mcp` bearer check, counted
  on the review's in-memory logs rather than a server's counter, since a
  republish or a restart builds new servers and the cursor must survive both — a route of its
  own beside `/mcp`, not an exception in `requireHuman`, serving nothing else,
  answered 401 by the Worker (and listed in Access's Bypass) like `/mcp`;
  `review_done`,
  `confirm` and `reopen` are human-only by construction. `author` is the
  resolved identity, never the body's claim (`server.Options.AuthorOf`).
  `--insecure-dev-identity` is the laptop escape hatch and is loopback-only.
  Caps and archiving (`hosted/limits.go`) keep a metered deployment bounded:
  `Limits` — bytes per document, documents per publish, revisions per review,
  publishes per agent per hour (sliding, injected clock), open reviews, stored
  bytes, and the two retentions — are enforced in `checkPublish` **before**
  anything is parsed or written, and a `LimitError` names the limit and its
  value. The stored total is in memory, never a listing: document bytes ride
  on `Meta.Bytes` as a running total, log bytes are measured by the
  `metered` layer between the write-through cache and the backend (once on
  the warm read, then per append, released on `Move`). It refuses publishes
  and never a human's note. `Sweep` (startup, then hourly) moves a review
  whole to `archive/` once its last activity is older than `RetainDone`
  (done) or `RetainOpen` (open) — never a delete, re-runnable if interrupted.
  Diagrams default off in hosted mode. Notifications (`hosted/notify.go`,
  `--notify-url`) fire on the agent's side of the loop only — publish,
  republish, a `reply`/`addressed` once the review is done — never on a
  human's action; carry title, agent, counts and the URL and never a word
  of a document or a note; are delivered off the request path (5s timeout,
  one retry, a failure is a stderr line, never a failed publish); and are
  debounced per review (one per minute, a burst collapses to "n updates",
  flushed by a ticker or `Flush()` in tests with an injected clock).
  Credentials come from env only, and
  nothing account-specific lives in this repo. The **image**
  (`Dockerfile`, goreleaser `dockers` + `docker_manifests`) is the released
  linux binary on `distroless/static:nonroot`, pushed to
  `ghcr.io/gruesomeparty/marginalia:<version>` (+ `latest`) for amd64 and
  arm64 on every release — one build path, nothing baked in; CI's `image`
  job builds it with a goreleaser snapshot and `scripts/image-smoke.sh`
  starts the amd64 one non-root with a `file:///tmp` store, checks
  `/healthz`, publishes over `/mcp`, and greps the history for secrets.
- `deploy/cloudflare` is the **deployment template**, the one place the repo
  ships infrastructure, and the split is the rule: what is generic lives
  here, and what is one account's — account id, hostname, bucket, Access
  AUD, every credential — never does. The hostname is `wrangler deploy
  --domain`, and the settings are a gitignored `settings.env` uploaded with
  `--secrets-file`. A Worker (`src/index.ts`) forwards every request to the
  single container via `getContainer` and passes it every `MARGINALIA_*`
  binding as its environment, so a setting a later release adds needs no
  template change. That works because the binary reads every setting but
  `--addr` (which the image's `CMD` sets) from env, `MARGINALIA_PUBLIC_URL`
  included (`publicOrigin` refuses anything but scheme and host). The Worker refuses only what the binary cannot know
  is wrong: no public URL, or a store that is not `r2://` (a container's
  disk dies with it). It also answers, without waking the container, the
  agent-path requests the host would only refuse — `/mcp` with no bearer,
  or with a wrong static token when no OAuth issuer is set (constant-time,
  by digest), and `/.well-known/*` the host does not serve — because those
  paths are public in Access and every forwarded request starts a sleeping,
  billed container. An anonymous `/healthz` is answered from the Durable
  Object's state (`awake()`, which neither starts the container nor renews
  its activity): `ok` or `ok (asleep)`; the static token buys the real
  check. Every other path is the reviewer's, and one arriving without
  Access's `Cf-Access-Jwt-Assertion` header (Access missing or
  misconfigured) gets a 403 at the edge; presence only, the host verifies
  the JWT. The host still checks every credential itself. The
  container sleeps after 30 minutes idle. An
  in-flight request, which includes an agent's long `await_review_done`,
  holds it awake. A cold start gets two minutes rather than the library's
  20s, because the host loads every review before it listens. The binary
  reads env once, at start, so the Durable Object keeps a hash of the
  settings the running container started with (`started-with`, never the
  values). The first request after a deploy that changed them restarts the
  container: SIGTERM, a drain, then a fresh start. Without this, a rotated
  token would wait for a sleep that a waiting agent postpones
  indefinitely. The container runs under the `durable_object` scheduling
  policy (Cloudflare refuses `default` on some accounts, and a policy is
  immutable once created), so the Durable Object names the image and the
  instance size when it starts it. `@cloudflare/containers` passes neither,
  so `Marginalia` hands the library a proxy of `ctx.container` whose
  `start()` adds them; the image reference is part of the settings hash, so
  a release also restarts the container. The
  `Dockerfile` is one `FROM ghcr.io/…:<version>` inside a release-please
  block (`extra-files`). Containers pull only from Cloudflare's registry,
  hence the rebuild. `deploy_template_test.go` pins that tag to
  `.release-please-manifest.json`, pins the `durable_object` scheduling
  policy and a single unnamed `getContainer` (that policy has no
  `max_instances`; one Durable Object is what keeps the host its store's
  single writer), and pins no
  `account_id`/`routes`/`vars` and no `workers_dev` or preview URL. It also
  checks that every name in `settings.example.env` is one the binary reads.
  CI's `cloudflare-template` job typechecks the Worker and runs a dry-run
  bundle, without the image build, since a release PR moves the tag before
  that image exists. `/mcp`, `/.well-known/oauth-protected-resource` and
  `/healthz` need an Access **Bypass** application: an agent cannot log in,
  and the host authenticates those paths itself.
- Reference implementation to generalize from: Black Mirror's
  `cmd/blackmirror/timebooking_review.go` + `timebooking_review.html`
  (event-sourced review server, atomic writes, replay) and the 2026-07-02
  spec-review artifact (static mode, localStorage + export). Marginalia is those
  two, made document-generic and lifted out of Black Mirror.

## Non-negotiable invariants

These are the design's failure modes — the PRD calls each one out explicitly:

- **Never mutate the source document.** Review is read-only against the input file.
  In hosted mode the source is a published snapshot: it is written once,
  put-if-absent, and a republish is a new revision beside it, never an edit.
- **Feedback events are append-only.** Never rewrite or delete lines in
  `<doc>.feedback.jsonl` — and `internal/storage` has no method that could,
  on any backend. Later events on the same block override earlier ones
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
  Hosted mode is the same page under `/r/{id}/`, not a second UI; it exists for
  the agent that shares no filesystem with the reviewer, and is opt-in.
- **An agent may answer and claim, never decide**, on every surface. Over MCP
  (stdio or HTTP) and under an agent identity in hosted mode, the only event
  types an agent can write are `reply` and `addressed`. `review_done`,
  `confirm` and `reopen` are the human's verdict, and the server enforces who
  is asking; a reviewer's `author` is the authenticated identity, not a field
  in the body.
- **No secrets in flags, issues or PRs.** Hosted credentials (MCP token, R2
  keys, JWT settings) come from environment variables only; flags show up in
  process lists. Issues and PRs quote document structure, never content.

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
button opens a summary sheet (issue #129: counts by type, what is unanswered,
an optional overall note) and only its **Submit** appends a `review_done`
event — the agent's signal to proceed, so one stray click must not send it.
The overall note is that event's `text`, not a new type and not a note on
some block. `require_verdict` withholds Submit until every commentable block
is answered.

## Agent feedback loop (PRD §8)

The tool improves itself: on an unsupported format or unknown flag, the binary's
**error message advertises the `request-feature` skill** (with tool version).
Agents capture freely (file `agent-feedback`-labeled GitHub issues, dedupe
first); Berkay gates what becomes work via an `approved-for-agent` label; an
agent session implements the approved queue on branches/PRs. Guardrail: quote
document *structure*, never *content* — no secrets or private text in issues.

**Write for two readers.** Issue and PR text is for Berkay: short and plain.
Everything for agents — context, plan, paths, acceptance detail, handoff state
— goes in a hidden HTML comment in the body, one block per purpose
(`<!-- agent:report … -->` from `request-feature`, `<!-- agent:triage … -->`
from triage, `<!-- agent:handoff … -->` on a pipeline PR), replaced in place
and never containing `-->`. Every skill and loop prompt that writes to GitHub
follows it. Berkay and every agent outside Actions post as the same account,
so an agent's comment is **marked**: Claude Code's footer ("Generated by/with
Claude Code") or any `<!-- agent:… -->` block. `scripts/comment-from.sh`
(tested in `internal/pipeline`) reads that mark — bot or marked is `agent`,
unmarked by a trusted login is `human`, anyone else `other` — and `triage.yml`
re-triages an issue labelled `needs-info`/`needs-decision` when a `human`
comments on it, so an answer to the loop's question is picked up. No Claude session or project links in commits, PRs or comments.

Triage is `skills/triage-issues/SKILL.md`, run by `.github/workflows/triage.yml`
on every opened/reopened issue and a six-hourly sweep. The agent there **only
reads**: it runs with read-only permissions and writes one verdict file per
issue, and a separate job with no agent applies them through
`scripts/apply-triage.sh` (tested in `internal/pipeline`), which owns the label
set — type, `priority:p0…p3`, `size:s|m|l`, `triaged`, `agent-ready`,
`needs-info`/`needs-decision`, `duplicate` — never touches `base:*` or `hold`,
and adds `approved-for-agent` only with the repository variable
`TRIAGE_AUTO_APPROVE=true`, for a ready, not-large issue by a trusted author
(the repo owner plus `TRIAGE_TRUSTED`). The repository is public; an issue's
text must not be able to approve or auto-merge anything, which is why that
decision is a script and not a prompt.

The third stage is `skills/implement-approved/SKILL.md` (`/marginalia:implement-approved`),
gated by `scripts/approved-queue.sh`: it reads `gh issue list --json` output and
names the oldest issue that states both an expected behaviour and an acceptance
criterion (exit 3 when the queue holds nothing implementable, so a run stops and
asks rather than guessing what "done" means). Rules the pipeline exists to keep:
**one issue → one PR based on the issue's base → stop** (never a second open PR,
never a stack on another `claude/*` branch — that is how work gets merged into a
dead branch), never merge or approve, **only the merge loop merges into
`main`, and only what its gate allows** (below) — an issue labelled `base:feature/<name>` targets that integration
branch instead, and `integration-automerge.yml` merges it there on green CI
through `scripts/automerge-check.sh`, which refuses `main` outright (tested in
`internal/pipeline`); the feature branch reaches `main` through one
human-reviewed PR, and `IMPLEMENT_CHAIN=true` (a repository variable) lets each
merge dispatch the next run,
match CI's pinned `golangci-lint` version (a linter built for an older Go
toolchain exits with a version error that reads like success), and after a merge
verify the files are on `main` and the issue actually closed rather than trusting
the PR's state. `.github/workflows/implement-approved.yml` runs it when an issue gains
`approved-for-agent`, when a `claude/*` PR merges, on a weekday cron and by
hand; the queue is ordered by triage's priority and skips `hold`. It is **off**
unless the repository variable `IMPLEMENT_LOOP` is `true`, at Berkay's request:
issues are implemented in project sessions for now, so every trigger starts a
run whose job is skipped, while triage, review and merge keep running. It is
also armed by credentials: without a `CLAUDE_CODE_OAUTH_TOKEN` or
`ANTHROPIC_API_KEY` secret every run (triage's too) skips with a notice.
`scripts/pick-model.sh` (tested in `internal/pipeline`) picks each run's
model from the work: triage Haiku; review Sonnet, Opus for a PR over 300
lines or touching storage, identity, MCP, hosted, review protocol, server,
deploy or PRD; the work loop Opus, Sonnet for a `size:s` issue. A
`model:opus|sonnet|haiku` label on the PR or issue wins.

The review loop is `skills/review-pr/SKILL.md`, run by
`.github/workflows/review-prs.yml` on every PR — Berkay's, an agent's, the work
loop's, a fork's — so every one carries exactly one of `review:pass`,
`review:concerns`, `review:blocking` and one comment, edited in place when the
head moves. It is triage's split again: the agent reads the PR as git objects
from a default-branch checkout (the PR's code is never checked out or run),
writes `review/<n>.json`, and a job with no agent applies it through
`scripts/apply-review.sh` (tested in `internal/pipeline`), which derives the
label from the findings' severities, refuses a verdict for a commit other than
the one fetched, and makes visible text inert (no mentions, links or comment
markers). The verdict also names the change's Cynefin domain (`clear`,
`complicated`, `complex`, `chaotic`) with reasons, recorded as `domain:` in
the comment's hidden block ahead of the agent's notes; a missing or unreasoned
low call is recorded as `complex`. The merge loop reads the first `domain:`
line of that block.
**No `pull_request_target`**: a fork's PR gets no secrets on
`pull_request`, so the two-hourly sweep reviews it from the default branch, and
finds any unreviewed head by the `sha:` in its own comment's hidden block.
Drafts and bots are skipped. A documentation-only PR (`scripts/ci-changes.sh`'s
`docs_only`: Markdown and `docs/`, never skills, `commands/`, `.claude/`, CLAUDE.md or the
PRD) runs no agent: the apply job re-checks the head's files and records a
scripted `review:pass`, domain `clear`, so the merge gate still sees a review
of the current head. It never approves, requests changes or blocks a
merge into `main`; `automerge-check.sh` refuses a `review:blocking` PR into a
feature branch, and the work loop's preflight answers a blocking or concerns
review on its open PR before it stops.

The last stage is the **merge loop** (`.github/workflows/merge-prs.yml`,
`scripts/merge-loop.sh`), at Berkay's request: not every PR needs a human, and
the loop decides which do. It runs no agent and never checks out a PR. For
each open PR into `main`, `scripts/merge-gate.sh` (tested in
`internal/pipeline`) answers `skip` (feature base, draft, `hold`, Renovate,
release-please, other bots), `wait` (CI not green on the head, a conflict, no
adversarial review *of the current head* — the review comment's `sha:` — or
`review:blocking`), `merge` (the maintainer approved this exact head, or
nothing asks for a human and `MERGE_LOOP_AUTO=true`), or `human`, listing
every reason: `review:concerns`, an untrusted author or a fork, a sensitive
path (workflows, scripts, the loop skills, deploy, `go.mod`, PRD — not
CLAUDE.md or `skills/review-doc`, which nearly every PR updates — the storage,
identity, MCP and review-protocol packages), a review whose domain is
`complex`, `chaotic` or missing (the domain only ever adds a reason), Go code
changed with no test changed, over 1,500 lines of code (tests, testdata and
docs not counted, from the REST file listing's per-file counts) or 40 files, a
breaking change, `needs-human`, or auto-merge being off — which is the
default, so out of the box every PR still waits for Berkay. A `human` PR's
diff and a cover page are published to the hosted instance
(`MARGINALIA_URL` + `MARGINALIA_MCP_TOKEN` secrets, `scripts/marginalia-call.sh`
over stateless `/mcp`) with `.github/merge-review.yaml`, whose `merge_pr` and
`request_changes` actions are the decision; `scripts/merge-verdict.sh` reads
only a human's events since that revision was published, after `review_done`.
A push republishes the same review (`review_id`), so notes re-anchor. Without
the secrets, or if publishing fails, a GitHub approval of the head by the
owner is the decision instead (it always counts). State is one sticky
`<!-- agent:merge v1 sha: review: published: state: -->` comment of
github-actions' own, and labels `merge:human`/`merge:changes`. A merge always
passes `--match-head-commit`; made with the workflow token it starts no push
workflow, so the loop dispatches `release.yml` (or merges with a
`MERGE_TOKEN` secret instead). It also runs on a push to `main` and flags
conflicts: a non-skipped PR GitHub reports `CONFLICTING` gets
`merge:conflict` and one `<!-- agent:merge-conflict v1 sha: -->` comment,
both cleared once it merges cleanly. It never rebases or resolves, and leaves
a PR that is merely behind alone (`main` does not require up to date).

Dependency updates are the one exception to the human gate on `main`, at
Berkay's request: Renovate (`renovate.json`) owns every ecosystem —
github-actions, gomod, the root `Dockerfile`, the Cloudflare template's npm —
and merges its own PR once every check is green, majors included, no review.
`platformAutomerge` is off so Renovate itself waits for the checks rather
than leaning on branch protection, and because the Renovate app merges, the
push to `main` runs CI and release-please. `deploy/cloudflare/Dockerfile` is
excluded: release-please moves that tag. Anything a person or an agent opens
still waits for review.

## Build order (PRD §9)

M1 server mode for markdown (`serve` + JSONL + `review_done` + skill doc +
feedback scaffold) → **M2 revision loop (hash-stale, resolution view) — done** →
**M3 trees: `.proto`, JSON/YAML/TOML, mermaid — done** → **M4 automated implementation
pipeline — in place (triage loop + skill + gate + work loop, the work loop paused behind `IMPLEMENT_LOOP`)**
→ **M5 static share mode (`export`/`import`) — done** → **M6 hosted mode
(#91: `mcp --http`, `internal/storage`, `host`, `publish_document`, identity,
OAuth, inbox, caps, R2, image, notifications, the `deploy/cloudflare`
template) — done**.

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
- Browser tests (`internal/browsertest`, issue #60) drive the real page with
  **chromedp**, chosen over Playwright so the suite stays Go, shares the
  repo's helpers, and needs no `package.json` in a single-binary project.
  They boot `session.Build` on a temp document behind `httptest` and assert on
  the DOM *and* the appended JSONL. No Chrome → skip; `MARGINALIA_BROWSER=require`
  (set on CI's ubuntu job) makes that a failure; `MARGINALIA_CHROME` names the
  binary. Diagrams use a stand-in `mmdc` that copies the committed real render,
  so no Node is needed. A PR that changes the page's JS brings a case. axe-core
  (`axe_test.go`, issue #155) audits a markdown page, the open composer, the
  Done sheet, a tree, a drawn diagram and the inbox, failing on serious or
  critical violations; the test fetches a pinned axe tarball from the npm
  registry, checks its sha256 and caches it — the page never loads it. A
  block that contains controls (a list, a fence, a tree node, a paragraph
  with a link) is `role="group"`, not `button`, and list items keep their
  native role. Prose
  blocks keep their notes and composer as the *next sibling* (`[data-block] +
  .existing`), not inside; diagram statements put theirs under the figure.
- Path-based CI: `ci.yml`'s `changes` job feeds the PR's file list to
  `scripts/ci-changes.sh` (tested in `internal/pipeline`), and each job runs
  only when its paths changed — a README edit runs none. A job skipped by its
  `if` reports success, which keeps the ruleset's required checks green; the
  `test` matrix instead runs both legs and skips their steps, since a skipped
  matrix job does not report under its per-leg names. CodeQL's required
  `analyze` gates the same way, `security.yml` (not required) uses a plain
  `paths:` filter. A change to CI itself, a failed listing or a push to
  `main` runs everything. A new required check must follow the same pattern,
  never a workflow-level `paths:`, which leaves the check pending forever.
- Coverage gate: `go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80`
- Lint: `golangci-lint run`
- Vulnerabilities: `security.yml` runs govulncheck (pinned, Renovate bumps it
  through the `# renovate:` comment) on PRs, `main` and weekly; it gates
  Renovate's automerge too.
- Skills live in `skills/`; the repo is its own Claude plugin marketplace (`.claude-plugin/`).
- Loop switches: `scripts/loop-vars.sh` shows and flips the repository
  variables the Actions loops read (`IMPLEMENT_LOOP`, `MERGE_LOOP_AUTO`, …),
  run with the maintainer's own `gh`; `.claude/skills/loop-vars` is its skill.
  A new `vars.NAME` in a workflow needs a row in its table, or CI fails.
