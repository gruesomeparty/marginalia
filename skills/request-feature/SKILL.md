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

Write for two readers. The visible body is for Berkay: what you tried and what
you expected, in two to four plain sentences. Everything an agent needs to act
on it — the exact invocation, the error, the workaround, the version, the
context, your guess at a fix — goes in one hidden block at the end, which
renders as nothing on GitHub:

```bash
gh issue create --repo gruesomeparty/marginalia \
  --label agent-feedback --label <category> \
  --title "<concise limitation>" \
  --body "$(cat <<'BODY'
<What you were trying to do and what got in the way, in plain words.>

**Expected:** <the behaviour you needed, one sentence>
**Acceptance:** <one checkable line: how anyone can tell it is done>

<!-- agent:report
invocation: marginalia <exact command>
actual: <error or behaviour, structure only>
workaround: <what you did instead, or none>
version: <marginalia version>
context: <project/harness — structure, not content>
suggested_fix: <files or approach, if you have one>
-->
BODY
)"
```

`<category>` is one of: `format-support`, `customization`, `skill-gap`, `bug`.
Text inside the block must not contain `-->`; write `- ->` if you have to quote
it. When you add a use case to an existing issue, the same split holds: one
visible sentence, the detail in an `<!-- agent:report … -->` block in the
comment.

## 3. What makes an implementable issue

An expected behaviour and a one-line acceptance criterion, visible, and the
exact invocation in the hidden block. Triage (`triage-issues`) labels the
issue, adds its own `<!-- agent:triage … -->` plan beside yours, and says when
it is ready; the maintainer gates work with the `approved-for-agent` label, and
a well-formed issue can be implemented from the issue alone.
