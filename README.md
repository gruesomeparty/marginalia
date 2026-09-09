# Marginalia

A document-review tool an agent hands to a human. It renders a markdown file, a
`.proto` schema, or a JSON/YAML/TOML tree as a readable page, collects inline
comments anchored to blocks, and writes them back as append-only JSONL feedback
events the agent consumes directly.

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

## Supported inputs

| Input | Block ID |
|---|---|
| `.md`, `.markdown` | `section/ordinal` — `5.3/2` |
| `.proto` (proto3 source) | schema path — `CreateOrderRequest/customer_id`, `CreateOrderRequest.Line/sku`, `OrderService/CreateOrder`, `Status/STATUS_UNSPECIFIED` |
| `.json`, `.yaml`, `.yml`, `.toml` | node path — `$.spec.storage.paths[2]`, `$["odd key"]`, `$doc[1].kind` (multi-document YAML) |

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
