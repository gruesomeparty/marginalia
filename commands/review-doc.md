---
description: Hand a document to a human for block-anchored review and consume the returned feedback events.
argument-hint: <path-to-doc>
---

Use the `review-doc` skill to run a Marginalia review for `$ARGUMENTS`.

Follow `skills/review-doc/SKILL.md` exactly: prefer the marginalia MCP tools
(`review_document`, or `publish_document` on a hosted instance) and wait with
`await_review_done` in a loop; fall back to `marginalia serve` and the
`<doc>.feedback.jsonl` log only when no server is connected. Answer questions
with `reply_to_note` as they come, then address every event grouped by block,
quoting `block` + `quote`, and record each change with `mark_addressed`.
