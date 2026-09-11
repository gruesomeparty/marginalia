# Marginalia

A document-review tool an agent hands to a human. It renders a markdown file, a
`.proto` schema, a JSON/YAML/TOML tree or a mermaid diagram — one document or a
whole set — as a
readable page, collects inline comments anchored to blocks, and writes them back
as append-only JSONL feedback events the agent consumes directly.

See `PRD.md` for the full product spec.

## Install

**Binary:**

```bash
go install github.com/gruesomeparty/marginalia@latest
```

Or download a release from the [releases page](https://github.com/gruesomeparty/marginalia/releases).

**Claude plugin** — add it from either marketplace, then install:

```
# Option A — this repo, as its own marketplace:
/plugin marketplace add gruesomeparty/marginalia

# Option B — the ghost-bazaar aggregator (all my plugins in one place):
/plugin marketplace add gruesomeparty/ghost-bazaar

# then, from whichever you added:
/plugin install marginalia
```

Then, in any session:

- `/marginalia:review-doc <path>` — hand a document to a human and consume the
  feedback events.
- `/marginalia:implement-approved` — drain one issue from this repo's
  `approved-for-agent` queue into a pull request (the loop that maintains
  Marginalia itself).

## Quickstart

```bash
marginalia serve README.md --open
```

Open the URL, click any block to comment, hit **Done** when finished. Every
comment is written to `README.md.feedback.jsonl` the instant it is saved — there
is no export step.

## Multi-document reviews

One server, one page per document, feedback in each document's own log:

```bash
marginalia serve docs/                       # every supported file beneath it
marginalia serve spec.md plan.md api.proto   # or name them
```

A sidebar tree mirrors the folders the documents were found in, with a live
comment count and a tick per finished file. **Done with this file** marks one
document reviewed; **Finish review set** marks them all — it appends
`{"type":"review_done","text":"session"}` to every log, so an agent watching any
one of them sees the handover close.

To curate what the reviewer sees, drop a `.marginalia.yml` in the directory:

```yaml
title: Ingest rework — sign-off
docs:
  - path: api/orders.proto
    label: Order service contract
  - path: docs/plan.md
    label: Rollout plan
  - deploy.yaml            # a bare path keeps its own name
```

With an index the list *is* the tree: its order, its labels, nothing else. That
makes it a whitelist too, so startup says how many supported files it left out —
and a mistyped entry fails loudly rather than quietly shrinking the review.

## Framing a review

The requesting agent decides what the review asks for, at serve time:

```yaml
# review.yaml — marginalia serve spec.md --config review.yaml
title: Ingest spec — retry policy
instructions: |
  Focus on the retry policy in §1. Ignore prose and wording.
  Tag every finding: Blocker for anything that must change before rollout.
actions:
  - type: blocker        # written to the log verbatim
    label: Blocker       # button text
    key: b               # optional keyboard shortcut on a focused block
  - type: nit
    key: n
  - type: finding
    requires_text: true
    fields:
      - name: severity
        options: [high, medium, low]
        required: true
      - name: owner
builtins: true           # false drops comment/suggest_edit/question/approve/reject
readonly:
  - "2"                  # section 2 is context: shown, muted, not commentable
require_verdict: false   # true: no review_done until every block is answered
```

You can also hand over per-block guidance and mark the parts you are not asking
about:

```yaml
notes:
  - doc: spec.md           # which document — required in a set, optional for one
    block: "1/2"
    text: why 500? I took it from the queue's batch limit — is that right?
skip:
  - "2"                  # generated appendix: folded, and refused if posted to
```

A note renders beside its block, labelled as coming from the requester;
answering it is an ordinary feedback event on that block, so nothing needs a
second channel. Give each note a `doc` when the review serves a set — block ids
are per-document, so an unnamed note follows its id into every document that
happens to have one; startup names any note whose document is not in the set. Notes never enter `<doc>.feedback.jsonl` — that log is the
human's answers, not your questions.

The instructions render as a banner above the document, so the framing survives
the switch from chat to browser. Each action is a button on every block, and an
action with nothing to fill in **saves on the tap** — most feedback is a verdict,
not prose, and a review where saying "nit" means typing is a review that gets
abandoned. An action with `fields` opens the composer with those values to pick.

The server enforces all of it: an event whose `type` is not in the configured
set is refused, so is one for a read-only block, so is a field that was not
declared or a choice outside its options. `GET /api/doc` echoes the
configuration, so the agent reading `blocker` out of the log can see what it
asked for.

## Supported inputs

| Input | Block ID |
|---|---|
| `.md`, `.markdown` | `section/ordinal` — `5.3/2`; a list item adds its position — `5.3/2.1`, nested `5.3/2.1.3` |
| `.proto` (proto3 source) | schema path — `CreateOrderRequest/customer_id`, `CreateOrderRequest.Line/sku`, `OrderService/CreateOrder`, `Status/STATUS_UNSPECIFIED` |
| `.json`, `.yaml`, `.yml`, `.toml` | node path — `$.spec.storage.paths[2]`, `$["odd key"]`, `$doc[1].kind` (multi-document YAML) |
| `.mmd`, `.mermaid`, and `mermaid` fences in markdown | what the statement connects — `client-->api`, `payments/worker-->queue`; in a fence, under the fence's own ID — `1/3/client-->api` |

Markdown lists are anchorable item by item: each bullet or numbered step is its
own block, nested ones included, so a note about one task lands on that task
instead of on the whole list. The list keeps its own anchor too, for a note
about its shape.

Everything but markdown renders as a folding tree, one commentable block per
node, indented by depth, with **Collapse all** for a big file.

- **`.proto`**: every declaration — message, field, `oneof` and its members,
  enum value, rpc, `reserved` range, option — is its own block, so `reject` on
  "field 4 was reused" and `suggest_edit` on a rename land on that exact field.
  Parsing is structural, not semantic: a schema whose imports aren't on disk, or
  that doesn't compile yet, still reviews fine.
- **`.json` / `.yaml` / `.toml`**: keys keep the order the author wrote them,
  scalars show their type (`port: "8080"` reads differently from `port: 8080`,
  which is the whole point of reviewing a config), and YAML comments render with
  the node they document.
- **mermaid**: every statement of a flowchart — node, edge, subgraph — is its
  own block, anchored by what it connects with the line style and any label
  left out (`worker -.retry.-> queue` anchors as `worker-->queue`). So "the
  retry edge should go to a dead-letter queue" lands on that edge and an agent
  can apply it mechanically. In markdown the fence's own `<pre>` is what you
  see — the diagram exactly as written, annotated in place — and the fence
  keeps its anchor for a note about the diagram as a whole. With
  [mermaid-cli](https://github.com/mermaid-js/mermaid-cli) (`mmdc`) installed
  the diagram is also **drawn**: rendered to SVG on the server, inlined into
  the page, and carrying the same anchors on its shapes, so clicking the retry
  arrow in the picture opens the composer for that statement — `Show source`
  flips back, and the choice sticks. No script and no request reach the page,
  which is what lets a shared file keep its picture under a strict CSP, and the
  drawing takes the reviewer's theme and light/dark. Without `mmdc` you get the
  anchored source, exactly as before — `--diagrams=off` asks for it on a
  machine that has one. A diagram type the parser does not take apart (a
  sequence diagram, say) stays reviewable as one source block rather than
  failing.

## Revising while the review is open

```bash
marginalia serve spec.md --watch
```

The server re-reads and re-parses a document when its file changes, so an edit
lands on the next page load without a restart — and prior feedback on a block
you changed comes back flagged stale, because the resolution view is
materialized per render.

The page never reloads under the reviewer's hands: a change while a comment is
half-typed shows a *"this document changed on disk — Reload"* prompt instead,
and auto-reloads only when nothing is open. Watching polls mtime and size a
couple of times a second rather than using filesystem notifications, which miss
the temp-file-and-rename that editors save with.

## How the page reads

- **Themes.** `--theme` picks a palette — `default`, `light`, `dark`,
  `catppuccin`, `catppuccin-latte`, `catppuccin-mocha`. A palette is a set of
  CSS variables and light/dark is a mode, so the theme you serve and the one
  the reviewer picks are the same mechanism. The **Display** menu on the page
  lets the reviewer choose System / Light / Dark and turn code ligatures off;
  those choices live in their browser, never on your disk.
- **Diagrams.** A mermaid diagram is drawn when
  [mermaid-cli](https://github.com/mermaid-js/mermaid-cli) is installed
  (`npm i -g @mermaid-js/mermaid-cli`, or point `--mmdc` at the binary), and
  shows its anchored source when it is not. The picture carries the anchors:
  hovering an arrow lights it up, clicking it comments on that statement, and a
  shape that already has notes wears the marker colour — the same badge the
  source shows. `Show source` flips every diagram on the page and the choice
  sticks in the reviewer's browser. `--diagrams=off` skips drawing entirely;
  renders are cached by content, so `--watch` does not restart a browser per
  keystroke, and a diagram that fails to draw falls back to its source instead
  of failing the page.
- **Code.** Fenced code blocks and `.proto` declarations are highlighted at
  render time, on the server — comments recede, strings and numbers stand out.
  Go, Rust, JS/TS, Python, shell, SQL, JSON, YAML, TOML, proto and HTTP are
  tokenized; anything else renders as plain text rather than badly. No
  highlighter is fetched: the page must survive a strict CSP, which is also
  why the mono font stack only *prefers* JetBrains Mono, Fira Code, Cascadia
  Code or Iosevka if the reviewer already has one, and falls back to the
  system mono otherwise.

## Sharing a review with someone off your machine

Server mode needs no export step — every comment is on disk the moment it is
saved — but it needs the reviewer to reach your machine. When they can't:

```bash
marginalia export spec.md -o review.html    # one self-contained file
# send review.html; they comment in their browser and send back a .json
marginalia import their-review.json         # merges onto spec.md.feedback.jsonl
```

The shared page loads **nothing**: inline CSS and JS, and every image reference
in the document is rewritten to say what it was rather than fetch it, so opening
the file makes no request at all. Comments live in that browser
(`localStorage`) until the reviewer hands them over through a pre-selected
textarea — no Clipboard API, which hosted contexts block — or the download
button, which says so if the viewer refuses it.

`import` is append-only and idempotent: it skips events already in the log, so
merging the same file twice changes nothing, and a malformed file is refused
before anything is written. Feedback already on disk travels with the exported
page, so a second round shows the thread so far.

## Reviewing again after a revision

Reopen a revised document and prior feedback re-anchors by block, with the
second pass made explicit:

- a note whose block still reads the same **stands** — the badge shows what was
  decided (green for approve, red for reject);
- a note written before that block changed is flagged **stale**, with the text it
  was written against (`was: "…"`) so you can see what moved;
- a note whose block is gone entirely is listed as **no longer anchored** at the
  end of the page instead of disappearing.

`GET /api/resolution` returns the same view as JSON — the current state per
block, its history, and the stale/orphaned flags — which is what an agent reads
before applying feedback:

```bash
curl -s localhost:8787/api/resolution | jq '.stale, .orphaned'
```

Nothing is rewritten to produce it: the log stays append-only and staleness is a
view over it, not a fact on disk.

## Applying the feedback

After a review, ask which `suggest_edit` replacements are safe to apply:

```bash
marginalia suggestions spec.md          # or a directory
marginalia suggestions spec.md --json   # for an agent to consume
```

A suggestion is **applicable** only when it is the note that stands for its
block, its hash still matches the block as the document now reads, and it
carries a hash at all. Everything else comes back under `needs_confirmation`
with the reason — the block changed since, a later note supersedes it, the block
is gone, or there is no hash to check.

Marginalia never edits your document: this reports, you apply.

## How it works

- Each block gets a stable ID (`section/ordinal` for prose, the node's own
  schema path for trees), a ~90-char quote, and a content hash — so feedback
  re-anchors on re-render and stale comments are flagged.
- Feedback events (`comment`, `suggest_edit`, `question`, `approve`, `reject`,
  `review_done`) are append-only JSONL beside the document.
- The page is fully self-contained (inline CSS/JS, system fonts, no external
  requests) and never touches the Clipboard API.

## Remote / phone review

Bind to your Tailscale interface and open the same URL from your phone:

```bash
marginalia serve doc.md --host 100.x.y.z
```

## Development

```bash
go test -race ./...      # tests
go build ./...           # build
marginalia serve X.md    # run
```
