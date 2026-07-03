---
name: review-doc
description: Use when you need a human to review a document you produced (spec, plan, PRD, ADR, report) and you want their feedback back as structured data. Renders the file as a block-anchored review page, waits for the human, then reads their feedback events.
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
marginalia serve <path> --open
```

This prints the URL and the feedback file path (`<path>.feedback.jsonl`). The
server writes every comment to that file the instant it is saved — there is no
export step.

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

## 4. Address the feedback

Read the JSONL, group events by `block`, and address every one explicitly.
**Quote `block` + `quote`** so the human can verify you anchored correctly:

> §5.3/2 ("A registry-backed contract mirroring…") — you asked to simplify this.
> Done: …

Event types: `comment`, `suggest_edit` (the `text` is the proposed replacement),
`question`, `approve`, `reject`. If a `hash` no longer matches the current block
(the doc changed since the comment), flag the note as **stale** and re-confirm
with the human before acting.

## 5. Revise and, if needed, re-review

If you change the document, offer another `marginalia serve` pass.

---

*Hit a limitation (unsupported format, missing flag, awkward workflow)? Invoke the
`marginalia:request-feature` skill to file it.*
