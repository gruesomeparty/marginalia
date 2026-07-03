---
name: request-feature
description: Use when Marginalia hits a limitation — an unsupported input format, a missing flag, or a workflow gap. Files a deduplicated, structured GitHub issue that the maintainer can triage and an agent can later implement.
---

# Requesting a Marginalia feature

Marginalia's primary users are agents, so limitations you hit are the feature
pipeline. File them precisely.

## Guardrails (read first)

- **Quote structure, never content.** Describe the document shape ("a markdown
  file with nested tables"), never paste private text or secrets.
- **At most one issue per limitation per session.**
- Link the session's *purpose*, not a transcript.

## 1. Dedupe

```bash
gh issue list --repo gruesomeparty/marginalia --label agent-feedback --state all --search "<keywords>"
```

If an issue already covers the gap, add a comment with your new use case instead
of filing a duplicate.

## 2. File the issue

Use the `agent-feedback` issue form:

```bash
gh issue create --repo gruesomeparty/marginalia \
  --label agent-feedback --label <category> \
  --title "<concise limitation>" \
  --body "<what you attempted (exact invocation), expected, actual/error, workaround, marginalia version, requesting context>"
```

`<category>` is one of: `format-support`, `customization`, `skill-gap`, `bug`.

## 3. What makes an implementable issue

Include the exact `marginalia` invocation, the expected behavior, the actual
behavior/error, and a one-line acceptance criterion. The maintainer gates work
with the `approved-for-agent` label; well-formed issues can be implemented from
the issue alone.
