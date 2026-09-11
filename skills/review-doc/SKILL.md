---
name: review-doc
description: Use when you need a human to review a document, schema, config or diagram you produced (spec, plan, PRD, ADR, report, .proto schema, JSON/YAML/TOML file, mermaid flowchart) and you want their feedback back as structured data. Renders the file as a block-anchored review page, waits for the human, then reads their feedback events.
---

# Reviewing a document with Marginalia

Once the marginalia plugin is installed, a human or agent can trigger this
workflow with `/marginalia:review-doc <path>`. This skill is the workflow
itself — follow it whether you were invoked that way or reached it directly.

## Preflight: ensure the binary exists

Run `marginalia version`. If it is not found, install it:

```bash
go install github.com/gruesomeparty/marginalia@latest
```

(or download a release binary from https://github.com/gruesomeparty/marginalia/releases).

## 1. Serve the document

```bash
marginalia serve <path> --open                 # one document
marginalia serve <dir> --open                  # every supported file beneath it
marginalia serve spec.md plan.md api.proto     # or name them
```

This prints the URL and the feedback file path (`<path>.feedback.jsonl`). The
server writes every comment to that file the instant it is saved — there is no
export step.

Supported inputs and how their blocks are anchored:

| Input | `block` looks like |
|---|---|
| `.md`, `.markdown` | `5.3/2` (section path + ordinal); list items add their position, `5.3/2.1` |
| `.proto` | `CreateOrderRequest/customer_id`, `CreateOrderRequest.Line/sku`, `OrderService/CreateOrder`, `Status/STATUS_UNSPECIFIED` |
| `.json`, `.yaml`, `.yml`, `.toml` | `$.spec.storage.paths[2]`, `$["odd key"]`, `$doc[1].kind` |
| `.mmd`, `.mermaid`, and `mermaid` fences in markdown | `client-->api`, `payments/worker-->queue`; inside a fence, under the fence's block — `1/3/client-->api` |

Anchoring by path is what makes feedback on a schema, config or diagram
mechanically applicable: the human's note points at the field, node or edge
itself, not at prose about it. A mermaid statement's anchor names what it
connects and leaves out the line style and any label, so `worker-->queue` is
the edge to change whether it was written `worker --> queue` or
`worker -.retry.-> queue`.

### Handing over several documents at once

A set is served as one server with a page per document and a navigation tree, so
the human gets one URL instead of one port per file. Each document still writes
to its own `<doc>.feedback.jsonl`, and the startup output lists every log — watch
all of them.

To choose what the reviewer sees and in what order, write a `.marginalia.yml`
next to the documents before serving:

```yaml
title: Ingest rework — sign-off
docs:
  - path: api/orders.proto
    label: Order service contract
  - docs/plan.md
```

The index is a whitelist: anything unlisted is invisible to the reviewer, and
startup reports how many supported files it excluded. Keep it in step with the
files you actually want signed off — a listed path that does not exist stops the
server rather than shrinking the review silently.

### Framing the review (optional)

If the review needs more than "read this", say so at serve time instead of only
in chat — the framing then travels with the page, which is where the human
actually is:

```yaml
# review.yaml — marginalia serve spec.md --config review.yaml
title: Ingest spec — retry policy
instructions: |
  Focus on the retry policy in §1. Ignore prose and wording.
  Tag every finding: Blocker for anything that must change before rollout.
actions:
  - type: blocker        # the word written to the log
    label: Blocker       # the button
    key: b               # optional shortcut on a focused block
  - type: finding
    requires_text: true
    fields:
      - name: severity
        options: [high, medium, low]
        required: true
builtins: true           # false drops comment/suggest_edit/question/approve/reject
readonly: ["2"]          # section 2 is context: shown, muted, not commentable
require_verdict: false   # true: no review_done until every block is answered
```

Use it when you want a **vocabulary** ("tag every finding") rather than prose: an
action with nothing to fill in saves on one tap, which is what makes a long
document reviewable at all. Ask for what you will actually act on — a required
field the human has to guess at is worse than no field.

The same file carries your own questions and the parts you are not asking about:

```yaml
notes:
  - block: "1/2"
    text: why 500? I took it from the queue's batch limit — is that right?
skip:
  - "2"                  # folded on the page, and refused if anything posts to it
```

A note renders beside that block, labelled as yours; the human's reply comes
back as an ordinary event on the same block. Prefer a note on the block over a
paragraph in `instructions` when the question is *about* something specific —
that is the whole point of anchoring. Your notes never enter the feedback log.

`--theme` picks a palette (`default`, `catppuccin`, `catppuccin-mocha`, …) if
the human has a preference; light/dark and ligatures are theirs to set on the
page, so do not try to serve those for them.

Startup prints the actions it accepted, and `GET /api/doc` echoes them, so you
can confirm the review was framed the way you asked.

## 2. Tell the human what you need

State the URL and exactly what you want reviewed ("I need your take on §3 and the
error-handling approach"). Then wait. Instructions in a config are the framing,
not a substitute for asking clearly.

## 3. Watch for completion

Poll `<path>.feedback.jsonl` for a `review_done` event:

```bash
tail -f <path>.feedback.jsonl
```

Completion signals, in priority order: a `{"type":"review_done"}` line; the human
says they're done; or the file goes quiet after activity.

Reviewing a set: poll every document's log. A `review_done` with
`"text":"session"` means the human pressed **Finish review set** — the whole
handover is done, so it appears in every log at once. A `review_done` with an
empty `text` means only that one document was marked done, and the others may
still be in progress.

## 4. Address the feedback

Read the JSONL, group events by `block`, and address every one explicitly.
**Quote `block` + `quote`** so the human can verify you anchored correctly:

> §5.3/2 ("A registry-backed contract mirroring…") — you asked to simplify this.
> Done: …

For a schema, config or data review, `block` names the declaration or node
directly (`CreateOrderRequest/customer_id`, `$.spec.replicas`) — quote that path
back, and apply a `suggest_edit` by replacing that one declaration or value, not
the file.

Event types: `comment`, `suggest_edit` (the `text` is the proposed replacement),
`question`, `approve`, `reject` — plus any action you configured, which arrives
as its own `type` (`blocker`, `nit`) with its structured values in `fields`
(`{"severity":"high"}`). You asked for that vocabulary, so treat it as binding:
a `blocker` is not a `comment`. If a `hash` no longer matches the current block
(the doc changed since the comment), flag the note as **stale** and re-confirm
with the human before acting.

## 5. Revise and, if needed, re-review

If you change the document, offer another `marginalia serve` pass — or serve it
with `--watch` in the first place, and your edits appear in the reviewer's page
(with prior notes on changed blocks flagged stale) without a restart. On the second
pass, read the materialized view rather than replaying the raw log:

```bash
curl -s localhost:8787/api/resolution
```

It gives, per block: the note that stands (`current`), its `history`, and two
flags that decide what you may act on —

- `stale: true` — the block was edited after that note was written. Do **not**
  apply it silently: the note quotes what it was written against, so say what
  changed and re-confirm with the human.
- `orphaned: true` — the block is gone from the document. The note survives with
  its `quote`; carry it to wherever that content went, or ask.

A non-stale `suggest_edit` is the one case you can apply verbatim: its `text` is
the replacement and the block still reads as the reviewer saw it. Ask the tool
which those are rather than working it out yourself:

```bash
marginalia suggestions <path> --json
```

It reads the document and its log from disk — no server needed — and splits the
`suggest_edit` events into `applicable` (the note that stands, with a hash that
still matches: apply the `replacement` verbatim over the `current` text) and
`needs_confirmation`, each with a `reason` — the block changed, the note has no
hash to check, a later note supersedes it, or the block is gone. Marginalia
never edits the document; applying is yours.

---

*Hit a limitation (unsupported format, missing flag, awkward workflow)? Invoke the
`marginalia:request-feature` skill to file it.*
