---
name: review-doc
description: Use when you need a human to review a document, schema or config you produced (spec, plan, PRD, ADR, report, .proto schema, JSON/YAML/TOML file) and you want their feedback back as structured data. Renders the file as a block-anchored review page, waits for the human, then reads their feedback events.
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

Anchoring by path is what makes feedback on a schema or config mechanically
applicable: the human's note points at the field or node itself, not at prose
about it.

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

## 2. Tell the human what you need

State the URL and exactly what you want reviewed ("I need your take on §3 and the
error-handling approach"). Then wait.

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
`question`, `approve`, `reject`. If a `hash` no longer matches the current block
(the doc changed since the comment), flag the note as **stale** and re-confirm
with the human before acting.

## 5. Revise and, if needed, re-review

If you change the document, offer another `marginalia serve` pass.

---

*Hit a limitation (unsupported format, missing flag, awkward workflow)? Invoke the
`marginalia:request-feature` skill to file it.*
