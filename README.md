# Marginalia

A document-review tool an agent hands to a human. It renders a markdown file, a
`.proto` schema, or a JSON/YAML/TOML tree — one document or a whole set — as a
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

Then `/marginalia:review-doc <path>` in any session.

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

## Supported inputs

| Input | Block ID |
|---|---|
| `.md`, `.markdown` | `section/ordinal` — `5.3/2`; a list item adds its position — `5.3/2.1`, nested `5.3/2.1.3` |
| `.proto` (proto3 source) | schema path — `CreateOrderRequest/customer_id`, `CreateOrderRequest.Line/sku`, `OrderService/CreateOrder`, `Status/STATUS_UNSPECIFIED` |
| `.json`, `.yaml`, `.yml`, `.toml` | node path — `$.spec.storage.paths[2]`, `$["odd key"]`, `$doc[1].kind` (multi-document YAML) |

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
