---
description: Serve a markdown document for human review and consume the returned feedback events.
argument-hint: <path-to-doc>
---

Use the `review-doc` skill to run a Marginalia review for `$ARGUMENTS`.

Follow `skills/review-doc/SKILL.md` exactly: start the server, tell the human the
URL and what you need reviewed, watch `<doc>.feedback.jsonl` for the `review_done`
event, then address every event grouped by block, quoting `block` + `quote`.
