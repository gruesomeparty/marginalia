# Marginalia

A document-review tool an agent hands to a human. It renders a markdown file as a
readable page, collects inline comments anchored to blocks, and writes them back
as append-only JSONL feedback events the agent consumes directly.

See `PRD.md` for the full product spec.

## Install

**Binary:**

```bash
go install github.com/suTerminus/marginalia@latest
```

Or download a release from the [releases page](https://github.com/suTerminus/marginalia/releases).

**Claude plugin:**

```
/plugin marketplace add suTerminus/marginalia
/plugin install marginalia
```

Then `/marginalia:review-doc <path>` in any session.

## Quickstart

```bash
marginalia serve README.md --open
```

Open the URL, click any block to comment, hit **Done** when finished. Every
comment is written to `README.md.feedback.jsonl` the instant it is saved — there
is no export step.

## How it works

- Each block gets a stable ID (`section/ordinal`, e.g. `5.3/2`), a ~90-char
  quote, and a content hash — so feedback re-anchors on re-render and stale
  comments are flagged.
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
