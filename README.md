# Marginalia

**A document-review tool an agent hands to a human.**

It renders a document — markdown, a `.proto` schema, a JSON/YAML/TOML tree, a
mermaid diagram, one file or a whole set — as a readable page, collects inline
comments anchored to blocks, and writes them back as append-only JSONL events
the agent consumes directly.

Three parts: **render, annotate, return events.** No database, no accounts, no
export step. Feedback lands beside the document as `<doc>.feedback.jsonl` the
instant it is saved.

Its primary users are agents. Its interface is a page a human enjoys using.

```
agent ──serve──▶ page ──human clicks a block──▶ <doc>.feedback.jsonl ──▶ agent
  ▲                                                                       │
  └──────────────── answers, revises, says what changed ──────────────────┘
```

`PRD.md` is the full product spec. This file is how to use what exists.

---

## Contents

- [What you get](#what-you-get) · [Install](#install) · [Quickstart](#quickstart)
- [Using it as an agent](#using-it-as-an-agent) · [Using it as a human](#using-it-as-a-human)
- [Framing a review](#framing-a-review) · [Supported inputs](#supported-inputs)
- [The review loop](#the-review-loop) · [Sharing off your machine](#sharing-off-your-machine)
- [Reference](#reference) · [Why it works this way](#why-it-works-this-way)
- [Gaps](#gaps) · [Direction](#direction) · [Development](#development)

---

## What you get

| | |
|---|---|
| **Block anchoring** | Every block carries `{block, quote, hash}`. Feedback re-anchors on re-render; a comment written against text that has since changed comes back flagged **stale** rather than silently pointing at the wrong thing. |
| **Five input families** | Markdown, `.proto`, JSON/YAML/TOML, mermaid, and unified diffs — including mermaid fences inside markdown. Everything but markdown renders as a folding, indented tree anchored by node path. |
| **Drawn diagrams** | With mermaid's CLI installed, a flowchart renders as a picture whose shapes carry the same anchors as the source. Click the arrow, comment on that edge. |
| **Framed reviews** | The requesting agent sets the instructions, the vocabulary (`blocker`, `nit`, …), structured fields, and which blocks are off-limits. Enforced server-side, not just rendered. |
| **Four shipped framings** | `adr`, `schema`, `security`, `copy` — so the same review compares across runs instead of getting a fresh vocabulary each time. |
| **Sentence-level notes** | Select a sentence in a paragraph and comment on *that*. The note survives unrelated edits around it and goes honestly stale when its own sentence changes. |
| **Threads** | A reviewer's question gets an answer, in place, under their own note. |
| **A revision loop** | The agent records *which note* a change answered; the reviewer confirms or reopens. The claim is checked against whatever the note is about — the block, or the one sentence. |
| **Long-document flow** | Progress as answered-of-commentable, keyboard movement between unanswered blocks, filters, and a Done button that names what is missing. |
| **Multi-document sets** | A directory, several paths, or a curated `.marginalia.yml`. One page per document, one log per document. |
| **An MCP server** | Seven tools over stdio, so an agent stops scraping banners and parsing JSONL. |
| **Share mode** | One self-contained HTML file that loads nothing, survives a strict CSP, and merges back append-only and idempotently. |
| **Themes** | Six palettes; the reviewer owns light/dark and ligatures. Code highlighted server-side, because a CDN script would not survive the CSP. |

---

## Install

```bash
brew install --cask gruesomeparty/tap/marginalia   # macOS
go install github.com/gruesomeparty/marginalia@latest
```

Or grab a build from the [releases page](https://github.com/gruesomeparty/marginalia/releases)
— Linux and macOS, amd64 and arm64. It is a single Go binary with no runtime
dependencies.

### Claude plugin

```
/plugin marketplace add gruesomeparty/marginalia   # or: gruesomeparty/ghost-bazaar
/plugin install marginalia
```

Then `/marginalia:review-doc <path>` hands a document to a human and consumes
the events. (`/marginalia:implement-approved` is the loop that maintains
Marginalia itself — you probably do not want it.)

### Optional: drawing mermaid diagrams

Marginalia needs no dependencies. If you want a `mermaid` fence to render as a
**picture** rather than as anchored source, install mermaid's own CLI:

```bash
npm i -g @mermaid-js/mermaid-cli     # provides `mmdc`
marginalia serve arch.md             # startup now says: "1 diagram(s) drawn"
```

Without it the page shows the anchored source and every other part of the
review is identical — this is never a prerequisite for handing a document over.

`mmdc` drives a headless Chromium through puppeteer, which is where the two
things that go wrong live:

- **Running as root** (containers, CI) — Chromium refuses without
  `--no-sandbox`, and says so. Give `mmdc` a puppeteer config:

  ```bash
  printf '{"args":["--no-sandbox","--disable-setuid-sandbox","--disable-dev-shm-usage"]}' > /tmp/puppeteer.json
  export MARGINALIA_MMDC_ARGS="-p /tmp/puppeteer.json"
  ```

- **No browser downloaded** — `Could not find chrome-headless-shell (ver. …)`
  means puppeteer never fetched one: `npx puppeteer browsers install
  chrome-headless-shell`. If Chromium is already on the machine, skip the
  download with `export PUPPETEER_EXECUTABLE_PATH=/path/to/chrome`.

`MARGINALIA_MMDC` (or `--mmdc`) names the binary when it is not on `PATH`. A
path that is wrong is an **error**, not a silent fallback to no pictures —
answering a typo with "install the thing you just said you had" would be worse.
`--diagrams=off` skips drawing entirely.

---

## Quickstart

```bash
marginalia serve README.md --open
```

Open the URL, click any block to comment, press **Done**. Every comment is on
disk the instant it is saved.

---

## Using it as an agent

The PRD says the primary users are agents, so there are two ways in and one of
them is much better.

### MCP (preferred)

**Installed as a Claude plugin, this is already done** — the plugin declares the
server, so the tools are there as soon as the `marginalia` binary is on `PATH`.
If the tools are missing, that binary is the thing to check: a server that
cannot start looks exactly like one nobody configured.

For any other MCP client:

```jsonc
// in your MCP client's config
{ "mcpServers": { "marginalia": { "command": "marginalia", "args": ["mcp"] } } }
```

Add `"--root", "<dir>"` to confine it somewhere other than the working
directory; `MARGINALIA_MCP_ROOT` does the same without a flag.

| Tool | What it does |
|---|---|
| `review_document` | Serve a document (or a set); returns the URL to hand over and the vocabulary you will read back. |
| `feedback_since` | Only what is new, by cursor. Every event carries the `note_id` you would answer it with. |
| `review_status` | The log materialized against the document as it now reads: per-block state, what went stale, what is orphaned, and which `suggest_edit`s are safe to apply verbatim. |
| `reply_to_note` | Answer one of the human's notes in place. |
| `mark_addressed` | Record that you changed the document in answer to a note. |
| `await_review_done` | Block until they finish. A timeout is not an error — it returns `done: false` so you can tell them you are still waiting. |
| `close_review` | Stop serving. Comments already saved stay on disk and stay readable. |

Three things about it are deliberate:

- **Reads come from the log on disk**, never from the running HTTP server. They
  keep working after your session ends, and any number of agents can follow one
  review by passing `paths` instead of a `session_id`.
- **An agent may answer and may claim, never decide.** `reply_to_note` and
  `mark_addressed` are the only tools that write, each writes one fixed type,
  and both name the note they are about. There is deliberately no confirm or
  reopen tool: that is the reviewer's verdict on your work, and an agent that
  could settle its own note would make the revision loop decorative. Everything
  else in the log is theirs — `review_done` above all, since it is the one
  signal the handover exists to produce.
- **`--root` confines every path a tool names.** A tool argument can come from
  text the model read, and `.yaml` is a supported input, so without a root
  "review `~/.config/gh/hosts.yml`" would render a token onto a listening
  socket. The check runs before anything is parsed or served.

### CLI

Everything the MCP server does, you can do with the binary:

```bash
marginalia serve spec.md --review schema      # hand it over
marginalia reply spec.md                      # what they said, and the ids
marginalia reply spec.md --to <id> --text …   # answer a question
marginalia addressed spec.md --to <id> --text … # say what you changed
marginalia suggestions spec.md --json         # what is safe to apply
```

`serve` prints the URL and the log path on startup, then runs until
interrupted. Feedback is readable from disk the whole time — you do not need to
stop the server to read it.

---

## Using it as a human

You get a page. Click a block, pick a verdict, done.

- **One tap where possible.** An action with nothing to fill in saves on the
  tap. Most feedback is a verdict, not prose, and a review where saying "nit"
  means typing is a review that gets abandoned.
- **Keyboard.** `Enter` on a focused block opens the composer. A configured
  action's key writes it without one. And on a long document:

  | | |
  |---|---|
  | `↓` / `↑` | next / previous block |
  | `⇧↓` / `⇧↑` | next / previous **unanswered** block, wrapping |
  | `j` `k` `n` `p` | the same, when the review has not claimed those letters |
  | `?` | what is actually bound right now |

  A configured action always wins its key — the agent asked for that
  vocabulary. That is why the arrows exist: a configured key is a single
  character, so `ArrowDown` can never be taken.

- **Comment on a sentence, not the paragraph.** Select text inside a block and
  an offer appears; take it and the note is about that sentence, which the page
  marks in the prose. Ignore it and selecting text is exactly what it always
  was — you can still copy a quote out, and clicking a block still comments on
  the block.
- **Progress that means something.** The header reads *answered of
  commentable*, not a raw comment count, because only that ratio says how much
  is left:

  ```
  12 of 61 answered · 12 comments · 3 to re-check · 2 outstanding
  ```

- **Filters** for how you actually work: not answered, ones you answered, ones
  the requester asked about, ones waiting to be re-checked. The menu label says
  how many blocks are out of view, so a filter is never a silent lie. An inline
  block — a list item, a diagram statement — dims instead of hiding, because
  removing a bullet renumbers the list around it.
- **Done tells you what is missing.** With `require_verdict`, it names the
  blocks still waiting and takes you to any of them, instead of only refusing.
- **Your display is yours.** The **Display** menu picks System / Light / Dark
  and turns code ligatures off. Those choices live in your browser, never on
  the agent's disk. `--theme` picks the palette it arrives in: `default`,
  `light`, `dark`, `catppuccin`, `catppuccin-latte`, `catppuccin-mocha`.

### Reviewing on your phone

There is no second UI. The server binds where you tell it, so bind it to your
Tailscale interface and open the same URL:

```bash
marginalia serve doc.md --host 100.x.y.z
```

---

## Framing a review

The requesting agent decides what the review asks for, at serve time.

### Shipped framings

Writing a config from scratch every time means the same question gets a
different vocabulary each time, and the events stop comparing across runs.

```bash
marginalia review list                        # names and their vocabularies
marginalia review show security > review.yaml # copy one to disk as a start
marginalia serve api.proto --review security  # or just use it by name
```

| Framing | Asks | Adds to the built-ins |
|---|---|---|
| `adr` | Is this decision sound? | `blocker`, `concern`, `alternative`, `context_missing` |
| `schema` | Is this change safe to ship? | `breaking` (with *what breaks*), `naming`, `optionality`, `migration` |
| `security` | Read it as an attacker. | `vulnerability` (with severity), `hardening`, `threat_model`, `out_of_scope` |
| `copy` | It goes in front of users. | `wrong`, `unclear`, `tone`, `typo` |

A preset is a starting point, not a cage: `--config` layers a file over it and
the file changes only what it names. `instructions:` alone reframes a review
without touching its verbs; naming `actions:` replaces the vocabulary wholesale.

### Writing your own

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
require_verdict: false   # true: no review_done until every block is answered

readonly:
  - "2"                  # section 2 is context: shown, muted, not commentable
skip:
  - "3"                  # generated appendix: folded, and refused if posted to

notes:                   # your questions, rendered beside the block
  - doc: spec.md         # which document — required in a set, optional for one
    block: "1/2"
    text: why 500? I took it from the queue's batch limit — is that right?
```

The instructions render as a banner above the document, so the framing survives
the switch from chat to browser. A note renders beside its block, labelled as
coming from the requester; answering it is an ordinary feedback event, so
nothing needs a second channel.

Give each note a `doc` when the review serves a set — block ids are
per-document, so `1/2` exists in every markdown file and an unnamed note would
follow its id into all of them. Startup names any note whose document is not in
the set rather than dropping it. **Notes never enter `<doc>.feedback.jsonl`** —
that log is the human's answers, not your questions.

**The server enforces all of it.** An event whose `type` is not in the
configured set is refused; so is one for a read-only block, a field that was not
declared, or a choice outside its options. Rendering a rule is not enforcing it,
and a tab left open across a restart must not be able to invent vocabulary.

---

## Supported inputs

| Input | Block ID |
|---|---|
| `.md`, `.markdown` | `section/ordinal` — `5.3/2`; a list item adds its position — `5.3/2.1`, nested `5.3/2.1.3` |
| `.proto` (proto3 source) | schema path — `CreateOrderRequest/customer_id`, `CreateOrderRequest.Line/sku`, `OrderService/CreateOrder`, `Status/STATUS_UNSPECIFIED` |
| `.json`, `.yaml`, `.yml`, `.toml` | node path — `$.spec.storage.paths[2]`, `$["odd key"]`, `$doc[1].kind` (multi-document YAML) |
| `.mmd`, `.mermaid`, and `mermaid` fences in markdown | what the statement connects — `client-->api`, `payments/worker-->queue`; in a fence, under the fence's own ID — `1/3/client-->api` |
| `.diff`, `.patch` | the file, and each hunk under it — `internal/server/handlers.go`, `internal/server/handlers.go/2` |

Everything but markdown renders as a folding tree — one commentable block per
node, indented by depth, with **Collapse all** for a big file.

**Markdown.** Lists are anchorable item by item: each bullet or numbered step is
its own block, nested ones included, so a note about one task lands on that task
rather than on the whole list. The list keeps its own anchor too, for a note
about its shape.

**`.proto`.** Every declaration — message, field, `oneof` and its members, enum
value, rpc, `reserved` range, option — is its own block, so `reject` on "field 4
was reused" and `suggest_edit` on a rename land on that exact field. Parsing is
structural, not semantic: a schema whose imports are not on disk, or that does
not compile yet, still reviews fine.

**`.json` / `.yaml` / `.toml`.** Keys keep the order the author wrote them,
scalars show their type (`port: "8080"` reads differently from `port: 8080`,
which is the whole point of reviewing a config), and YAML comments render with
the node they document.

**mermaid.** Every statement of a flowchart — node, edge, subgraph — is its own
block, anchored by what it connects with the line style and any label left out
(`worker -.retry.-> queue` anchors as `worker-->queue`). So "the retry edge
should go to a dead-letter queue" lands on that edge and an agent can apply it
mechanically. In markdown the fence's own `<pre>` is what you see — the diagram
exactly as written, annotated in place — and the fence keeps its anchor for a
note about the diagram as a whole.

With `mmdc` installed the diagram is also **drawn**: rendered to SVG on the
server, inlined into the page, carrying the same anchors on its shapes. Clicking
the retry arrow in the picture opens the composer for that statement; hovering
lights it up; a shape that already has notes wears the marker colour. **Show
source** flips every diagram on the page and the choice sticks. No script and no
request reach the page, which is what lets a shared file keep its picture under
a strict CSP, and the drawing takes the reviewer's theme and light/dark.

A diagram type the parser does not take apart — a sequence diagram, say — stays
reviewable as one source block rather than failing.

**Diffs and patches.** The thing a human is most often asked to approve, so the
review is the *change* rather than the file. Each file is a top-level block and
each hunk hangs off it, with the commit message from `git format-patch` kept as
a block of its own — it is part of what is being signed off.

```bash
git diff main... > change.patch && marginalia serve change.patch
```

Hunks are anchored by **ordinal, not line number**: regenerating a patch after
an earlier hunk changes shifts every line number below it, which would orphan
every note under it. The markers are hashed with the text, because `+ if n >
2000` and `- if n > 2000` are opposite statements. A note on the *file* —
"this shouldn't be in the patch at all" — hashes the file's identity and tally
rather than its hunks, so editing a hunk does not make it stale.

Since the review config's patterns already treat `/` as a separator,
`readonly: ["internal/"]` mutes a whole directory of a patch.

Parsing is tolerant, like every other format here: a truncated hunk, a patch
from something that is not git, a hunk with no file header, or a mailer that
ate the leading space off context lines all still render. A binary file, a pure
rename or a mode change says what it is instead of showing an empty diff.

### Where sentence-level notes apply

Deliberately narrow: markdown prose. A tree node and a diagram statement are
already fine-grained, and inside a code fence the rendered text and the block's
plain text can differ — so the page simply does not offer there, and a note on
the whole block is what you get. That is better than an anchor nothing can
resolve.

### Code in a document

Fenced code blocks and `.proto` declarations are highlighted at render time, on
the **server**: comments recede, strings and numbers stand out. Go, Rust,
JS/TS, Python, shell, SQL, JSON, YAML, TOML, proto and HTTP are tokenized;
anything else renders as plain text rather than badly.

No highlighter is fetched — the page has to survive a strict CSP. That is also
why the mono stack only *prefers* JetBrains Mono, Fira Code, Cascadia Code or
Iosevka if the reviewer already has one, and falls back to the system mono.

### Multi-document sets

```bash
marginalia serve docs/                       # every supported file beneath it
marginalia serve spec.md plan.md api.proto   # or name them
```

A sidebar tree mirrors the folders the documents were found in, with a live
comment count and a tick per finished file. **Done with this file** marks one
document; **Finish review set** marks them all, appending `review_done` to every
log so an agent watching any one of them sees the handover close.

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

---

## The review loop

### 1. Hand it over

```bash
marginalia serve spec.md --review schema --watch
```

`--watch` re-reads and re-parses when the file changes, so an edit lands without
a restart. The page never reloads under the reviewer's hands: a change while a
comment is half-typed shows a *"this document changed on disk — Reload"* prompt
and auto-reloads only when nothing is open. Watching polls size and mtime a
couple of times a second rather than using filesystem notifications, which miss
the temp-file-and-rename that editors save with.

### 2. Read what they said

```bash
curl -s localhost:8787/api/resolution | jq '.outstanding, .stale, .orphaned'
```

The resolution view is the log replayed against the document **as it now
reads** — not the raw log:

- a note whose block still reads the same **stands**, and the badge shows what
  was decided;
- a note written before that block changed is **stale**, with the text it was
  written against (`was: "…"`) so you can see what moved;
- a note whose block is gone entirely is listed as **no longer anchored** at the
  end of the page instead of disappearing.

Nothing is rewritten to produce it. Staleness is a reading of the log, not a
fact on disk.

### 3. Answer their questions

A question used to be a dead end — you could revise the document and hope they
noticed.

```bash
marginalia reply spec.md                         # the notes, and the ids
marginalia reply spec.md --to 8f2a1c4b9de0 --text "The cap comes from the upstream API."
```

The answer hangs under their own note the next time the page renders, and the
page shows a **Reply** box under every note so they can answer back. A reply
changes nothing about the note it answers: the question stays the block's
current state, and the header count still says how much the *reviewer* said.
Threads are one level deep on purpose — an answer to an answer re-roots to the
note that started it.

Note ids are **derived** from the event, so every log already on disk has them
and an id survives `export`/`import` unchanged.

### 4. Revise, and say which note you were answering

```bash
marginalia addressed spec.md --to 8f2a1c4b9de0 --text "Raised the cap to 2000."
```

Their next look shows that block as **addressed — is this right now?**, with
their original note beside what the block now says, and two buttons: *Yes,
settled* or *No, still open*. The second round is a short queue instead of a
full re-read.

**The claim is checkable, not just asserted.** If a re-render finds that the
thing the note is about has not moved, the page says so next to the claim — the
edit may be somewhere else, or may not have happened. The claim is still shown;
this is a review tool, not a court.

What gets checked is what the note is about: the block's hash for a block-level
note, and the *sentence* for a sentence-level one. So editing a different
sentence in the same paragraph does not make an unrelated claim look borne out.

A note nobody addressed stays **outstanding**, however much the document moves
around it: silence is not resolution. An `approve` is not outstanding work.
Confirming and reopening are the reviewer's, and there is no tool for them.

### 5. Apply what is safe

```bash
marginalia suggestions spec.md --json
```

A `suggest_edit` is **applicable** only when it is the note that stands for its
block, its hash still matches the block as the document now reads, *and* it
carries a hash at all. Everything else comes back under `needs_confirmation`
with the reason — the block changed since, a later note supersedes it, the block
is gone, or there is no hash to check.

Marginalia never edits your document. This reports; you apply.

---

## Sharing off your machine

Server mode needs no export step — but it needs the reviewer to reach your
machine. When they cannot:

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
before anything is written. A reply whose target is in neither the incoming file
nor the existing log is refused too — that is the last moment the mismatch can
be reported to someone who still has the review it answers. Feedback already on
disk travels with the exported page, so a second round shows the thread so far.

---

## Reference

### Commands

| | |
|---|---|
| `serve <doc\|dir>...` | Serve one or more documents for review |
| `export <doc>` | Write a self-contained review page for someone off your machine |
| `import <review.json>` | Merge a shared review back onto the document's log |
| `reply <doc>` | List the notes and their ids; `--to`/`--text` answers one |
| `addressed <doc>` | Record that you acted on a note, so the reviewer can check it |
| `suggestions <doc\|dir>...` | The `suggest_edit`s that are safe to apply verbatim |
| `review list` / `review show <name>` | Inspect the shipped framings |
| `mcp` | Speak MCP over stdio |
| `version` | Print version information |

### Key flags

| Flag | On | Meaning |
|---|---|---|
| `--port` / `--host` | `serve` | Where to listen. Default `127.0.0.1:8787`; set the host to your Tailscale IP for remote review. |
| `--open` | `serve` | Open the page in a browser |
| `--watch` | `serve` | Re-parse when the file changes; the page offers a reload |
| `--config <yaml>` | `serve`, `export` | Framing, vocabulary, read-only blocks |
| `--review <name>` | `serve`, `export` | Start from a shipped framing |
| `--theme <name>` | `serve`, `export` | Palette the page arrives in |
| `--diagrams auto\|off` | `serve`, `export` | Draw mermaid diagrams |
| `--mmdc <path>` | `serve`, `export` | Where mermaid-cli lives |
| `--author <name>` | `serve`, `export`, `reply`, `addressed` | Recorded on events; defaults to `$USER` |
| `-o, --out` | `export` | Output file; default `<doc>.review.html` |
| `--doc <path>` | `import` | Which log to append to, when the file does not say |
| `--root <dir>` | `mcp` | Confine every path a tool call may reach |
| `--json` | `suggestions`, `reply` | Machine-readable output |

### Environment

| | |
|---|---|
| `MARGINALIA_MCP_ROOT` | Default `--root` for `mcp` |
| `MARGINALIA_MMDC` | Path to mermaid-cli |
| `MARGINALIA_MMDC_ARGS` | Extra arguments for it (a puppeteer config, say) |

### HTTP API

| | |
|---|---|
| `GET /` | The first document |
| `GET /d/{rel...}` | One page per document |
| `GET /api/doc[?doc=]` | The parsed document, the log, the resolution view and the review config |
| `GET /api/feedback[?doc=]` | The raw log |
| `POST /api/feedback[?doc=]` | Append one event (capped at 1 MB; 413 past it) |
| `GET /api/resolution[?doc=]` | The materialized view — per-block state, stale, orphaned |
| `GET /api/revision` | The re-parse counter, for `--watch` |
| `POST /api/session_done` | `review_done` on every document in the set |

A posted event names its own document, and the server only writes to documents
in the served set — a stale page must not be able to append elsewhere.

### Event schema

```jsonc
{
  "doc": "spec.md",
  "block": "5.3/2",          // section path + ordinal, or a node/statement path
  "quote": "The first ~90 characters of the block…",
  "hash": "9f2a1c4b9de0",    // sha256(normalized text)[:12] — how staleness is detected
  "type": "blocker",
  "text": "…",
  "fields": {"severity": "high"},
  "author": "berkay",
  "ts": "2026-09-16T08:11:15Z",
  "reply_to": "8f2a1c4b9de0", // on reply/addressed/confirm/reopen: the note this is about
  "sub": {                    // present only when the note is about one sentence
    "quote": "Retries use no backoff.",
    "prefix": "The cap is 500. ",
    "suffix": " Failures go to the log.",
    "start": 16,
    "hash": "c814be3e3d44"
  }
}
```

**Sub-anchors are a field, never a longer block id.** A sub-block id
(`5.3/2#1`, say) would be accepted by every consumer that already reads
`block`, and would silently mean something different to each of them. An event
with a `sub` still names its block exactly as before, so a reader that knows
nothing about this sees an ordinary note on the paragraph — which is where it
is, just less precisely.

Re-location is by **quote plus surrounding context**, the selectors the W3C
annotation model settled on for the same problem. An offset alone would always
"find" something, which is the failure mode worth avoiding: a note that
silently re-anchors to a neighbouring sentence is worse than one that admits it
is stale. So a sub-anchored note is judged by whether **its own sentence** is
still there, not by whether the paragraph changed — otherwise the feature would
be worthless on any paragraph anyone edits.

The page sends only the quote; the server derives the context and the offset
against its own copy of the block, so the two cannot disagree about text the
reviewer is looking at. A quote the block does not contain is refused rather
than stored as an anchor nothing could ever resolve.

**Types.** `comment`, `suggest_edit` (the `text` is the replacement),
`question`, `approve`, `reject`, plus any action the requesting agent
configured. The protocol's own events are `review_done`, `reply`, `addressed`,
`confirm` and `reopen` — a config can neither define nor disable those.

A note's **status** (`outstanding` / `addressed` / `confirmed` / `reopened`) and
its **staleness** are *derived* by replaying the log. Neither is a field anyone
writes, and nothing in the log is ever rewritten.

### Files on disk

| | |
|---|---|
| `<doc>.feedback.jsonl` | The log. Append-only, beside the document. |
| `.marginalia.yml` | Optional, in a served directory: whitelist, order, labels. |
| user cache dir | Drawn SVGs, keyed by source hash. Never beside your document. |

---

## Why it works this way

These are the constraints the design is built on. They explain most of the
choices above, and none of them is negotiable:

- **The source document is never mutated.** Review is read-only against the
  input. `suggestions` reports; the caller applies.
- **Feedback events are append-only.** Later events override earlier ones only
  when *materializing* a view. The log itself is immutable and replayed
  chronologically.
- **No export step in the local loop.** In server mode every comment is on disk
  the instant it is saved. A flow that makes the human copy, download or paste
  is a failure — tolerated only in share mode, where there is no alternative.
- **Never depend on the Clipboard API.** Hosted and embedded contexts block it.
  Every copy affordance degrades to a pre-selected textarea.
- **Self-contained HTML in both modes.** Inline CSS and JS, system fonts, no
  external requests. Share mode must survive a strict CSP — which is why code is
  highlighted on the server and diagrams are inlined SVG.
- **Server mode is the product.** Share mode exists for people off your machine,
  not as a second UI. The phone opens the same URL.
- **No database.** Documents in, HTML out, JSONL beside the source.

---

## Gaps

Honest limitations of what is on `main` today, in the order they matter. Each
links to where it is tracked.

| Gap | Detail |
|---|---|
| **Limited input formats** ([#52](https://github.com/gruesomeparty/marginalia/issues/52), [#31](https://github.com/gruesomeparty/marginalia/issues/31)) | Diffs have landed. Still missing: OpenAPI anchored by operation rather than as a generic tree, HCL, SQL migrations, and source code as symbols. What Marginalia accepts is what it is for, so this stays the first-order gap. |
| **Only flowcharts decompose** | Other mermaid diagram types stay reviewable as a single source block rather than per statement. |
| **The page's JavaScript has no tests** ([#60](https://github.com/gruesomeparty/marginalia/issues/60)) | ~300 Go tests, zero browsers. Composer behaviour, diagram clicks, keyboard movement and share-mode storage are verified by hand, not by CI. The interface is half the product and the untested half. |
| **Touch and accessibility are incomplete** ([#50](https://github.com/gruesomeparty/marginalia/issues/50)) | The drawn diagram is effectively mouse-only, and the page has had no keyboard or screen-reader audit. |
| **`suggest_edit` is reported, never applied** ([#55](https://github.com/gruesomeparty/marginalia/issues/55)) | By design Marginalia will not edit your document — but the agent's half of that loop is still manual. |
| **No blocking handoff from the CLI** ([#44](https://github.com/gruesomeparty/marginalia/issues/44)) | MCP has `await_review_done`. There is no `serve --until-done`, no long-poll endpoint, and no outbound notification. |
| **Drawing needs Node** | `mmdc` is the only non-Go dependency anywhere near this tool. It is optional and degrades to anchored source. |

---

## Direction

Two things come first, in this order, because they are what the tool *is*:

**1. What it accepts.** Marginalia is worth reaching for exactly as often as it
can read the thing you want reviewed. Every format it cannot take is a review
that happens somewhere worse. More inputs, and finer anchoring within the ones
it already has, is the first call on the time
([#52](https://github.com/gruesomeparty/marginalia/issues/52),
[#31](https://github.com/gruesomeparty/marginalia/issues/31)).

**2. The interface.** The page is the whole product from the human's side, and
it is the part with no automated tests
([#60](https://github.com/gruesomeparty/marginalia/issues/60)) and no
accessibility pass ([#50](https://github.com/gruesomeparty/marginalia/issues/50)).
A reviewer who finds it tiring stops reviewing, and no amount of event schema
fixes that.

After those, in no fixed order: the agent's half of `suggest_edit`
([#55](https://github.com/gruesomeparty/marginalia/issues/55)) and a blocking
handoff from the CLI ([#44](https://github.com/gruesomeparty/marginalia/issues/44)).

The [tracker](https://github.com/gruesomeparty/marginalia/issues) is the live
roadmap; every milestone in the PRD's build order has shipped.

### Deliberately not now

Marginalia is a local tool. You run it on your machine and hand the URL to
someone who can reach your machine — usually you, sometimes a colleague on the
same Tailnet, and otherwise through `export`, which needs no server at all.

Everything that would follow from *hosting* a review instead — authentication
and provenance ([#54](https://github.com/gruesomeparty/marginalia/issues/54)),
more than one reviewer on one document
([#53](https://github.com/gruesomeparty/marginalia/issues/53)) — is downstream
of a decision that has not been made and may never be. So they are not on the
list above, and their absence is a scope choice rather than a gap:

- There is **no authentication**. Anyone who can reach the URL can write to the
  log, and `author` is whatever the client says it is. That is fine for
  `127.0.0.1` and for a Tailnet; it is not fine on an untrusted network, and
  Marginalia will not pretend otherwise by growing a token flag.
- There is **one reviewer**. Two people on one document write to one log with no
  identity and no conflict story.

If remote review ever becomes the point, both become real work. Until then,
building auth for a threat model nobody has would be the expensive kind of
speculation — and share mode already covers the case it would serve.

### How work gets picked up

Marginalia improves itself. On an unsupported format or an unknown flag, the
binary's error message points at the `request-feature` skill, so an agent that
hits a wall files it. Agents capture freely as `agent-feedback` issues;
`approved-for-agent` is the gate that turns one into work; `/marginalia:implement-approved`
drains the approved queue into a pull request. The guardrail is that an issue
quotes document **structure**, never **content** — no secrets, no private text.

---

## Development

```bash
go build ./...                                          # build
go test -race ./...                                     # tests
go test -run TestName ./internal/document/              # one test
go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80
golangci-lint run                                       # matches CI's pinned version
go run . serve <doc.md> --open                          # run it
```

`CLAUDE.md` is the orientation for agents working *on* Marginalia — architecture,
the invariants above, and the traps that are easy to fall into. `PRD.md` is
authoritative when the two disagree.
