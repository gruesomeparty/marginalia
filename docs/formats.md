# Supported formats

The format comes from the file extension. Every block gets an id derived from
the document's own structure, so a note stays on the thing it was about when
the document around it changes.

| Input | Block ID |
|---|---|
| `.md`, `.markdown` | `section/ordinal` — `5.3/2`; a list item adds its position — `5.3/2.1`, nested `5.3/2.1.3` |
| `.proto` (proto3 source) | schema path — `CreateOrderRequest/customer_id`, `CreateOrderRequest.Line/sku`, `OrderService/CreateOrder`, `Status/STATUS_UNSPECIFIED` |
| `.json`, `.yaml`, `.yml`, `.toml` | node path — `$.spec.storage.paths[2]`, `$["odd key"]`, `$doc[1].kind` (multi-document YAML) |
| an OpenAPI or Swagger spec in any of those | an operation by method and path — `GET /orders/{id}`, below it `GET /orders/{id}.responses["200"]`; a path item by its path; a component by kind and name — `components/schemas/Order`; the rest as above |
| `.mmd`, `.mermaid`, and `mermaid` fences in markdown | what the statement connects — `client-->api`, `payments/worker-->queue`; a message `C->A` (repeats `C->A#2`), a transition `Pending-->Paid`, a relationship `CUSTOMER--ORDER`; in a fence, under the fence's own ID — `1/3/client-->api` |
| `.diff`, `.patch` | the file, and each hunk under it — `internal/server/handlers.go`, `internal/server/handlers.go/2` |
| `.sql`, and `sql` fences in markdown | ordinal, kind and object of each statement — `3:alter-table:orders`, `1:begin`; in a fence of two or more statements, under the fence's own ID — `1/3/2:create-index:orders` |
| `.tf`, `.tfvars`, `.hcl` | a block's type and labels, what is inside it after a slash — `resource.aws_s3_bucket.logs`, `resource.aws_s3_bucket.logs/versioning/enabled`, `variable.region/default` |

Everything but markdown renders as a folding tree — one commentable block per
node, indented by depth, with **Collapse all** for a big file.

## Markdown

Lists are anchorable item by item: each bullet or numbered step is
its own block, nested ones included, so a note about one task lands on that task
rather than on the whole list. The list keeps its own anchor too, for a note
about its shape.

## Protocol Buffers

Every declaration — message, field, `oneof` and its members, enum
value, rpc, `reserved` range, option — is its own block, so `reject` on "field 4
was reused" and `suggest_edit` on a rename land on that exact field. Parsing is
structural, not semantic: a schema whose imports are not on disk, or that does
not compile yet, still reviews fine.

## JSON, YAML and TOML

Keys keep the order the author wrote them,
scalars show their type (`port: "8080"` reads differently from `port: 8080`,
which is the whole point of reviewing a config), and YAML comments render with
the node they document.

## Mermaid

Every statement of a flowchart — node, edge, subgraph — is its own
block, anchored by what it connects with the line style and any label left out
(`worker -.retry.-> queue` anchors as `worker-->queue`). So "the retry edge
should go to a dead-letter queue" lands on that edge and an agent can apply it
mechanically. In markdown the fence's own `<pre>` is what you see — the diagram
exactly as written, annotated in place — and the fence keeps its anchor for a
note about the diagram as a whole.

With `mmdc` installed ([how](#drawing-mermaid-diagrams)) the diagram is also **drawn**: rendered to SVG on the
server, inlined into the page, carrying the same anchors on its shapes. Clicking
the retry arrow in the picture opens the composer for that statement; hovering
lights it up; a shape that already has notes wears the marker colour. **Show
source** flips every diagram on the page and the choice sticks. No script and no
request reach the page, which is what lets a shared file keep its picture under
a strict CSP, and the drawing takes the reviewer's theme and light/dark.

Sequence, state and ER diagrams are taken apart the same way: a message by
who sends it to whom (`C->A`, the second one between the same pair `C->A#2`),
a transition by what it connects (`Pending-->Paid`), a relationship by its
pair of entities (`CUSTOMER--ORDER`), with groups, composite states and
entity attributes underneath. Their drawings are clickable too: a
participant, state or entity by name, and a message, note, loop, transition or
relationship by its place in the source when the picture draws as many as the
source writes. What cannot be paired that surely (a state edge inside a
composite state, an entity's attributes) stays one toggle away in the source. A diagram type the parser does not take
apart — a class diagram, say — stays reviewable as one source block rather
than failing.

## Diffs and patches

The thing a human is most often asked to approve, so the
review is the *change* rather than the file. Each file is a top-level block and
each hunk hangs off it, with the commit message from `git format-patch` kept as
a block of its own — it is part of what is being signed off.

```bash
git diff main... > change.patch && marginalia serve change.patch
```

Hunks are anchored by **ordinal, not line number**: regenerating a patch after
an earlier hunk changes shifts every line number below it, which would orphan
every note under it. The markers are hashed with the text, because `+ if n >
2000` and `- if n > 2000` are opposite statements. A note on the *file* —
"this shouldn't be in the patch at all" — hashes the file's identity and tally
rather than its hunks, so editing a hunk does not make it stale.

Since the review config's patterns already treat `/` as a separator,
`readonly: ["internal/"]` mutes a whole directory of a patch.

Parsing is tolerant, like every other format here: a truncated hunk, a patch
from something that is not git, a hunk with no file header, or a mailer that
ate the leading space off context lines all still render. A binary file, a pure
rename or a mode change says what it is instead of showing an empty diff.

## Drawing mermaid diagrams

Marginalia needs no dependencies. If you want a `mermaid` fence to render as a
**picture** rather than as anchored source, install mermaid's own CLI:

```bash
npm i -g @mermaid-js/mermaid-cli     # provides `mmdc`
marginalia serve arch.md             # startup now says: "1 diagram(s) drawn"
```

Without it the page shows the anchored source and every other part of the
review is identical — this is never a prerequisite for handing a document over.

`mmdc` drives a headless Chromium through puppeteer, which is where the two
things that go wrong live:

- **Running as root** (containers, CI) — Chromium refuses without
  `--no-sandbox`, and says so. Give `mmdc` a puppeteer config:

  ```bash
  printf '{"args":["--no-sandbox","--disable-setuid-sandbox","--disable-dev-shm-usage"]}' > /tmp/puppeteer.json
  export MARGINALIA_MMDC_ARGS="-p /tmp/puppeteer.json"
  ```

- **No browser downloaded** — `Could not find chrome-headless-shell (ver. …)`
  means puppeteer never fetched one: `npx puppeteer browsers install
  chrome-headless-shell`. If Chromium is already on the machine, skip the
  download with `export PUPPETEER_EXECUTABLE_PATH=/path/to/chrome`.

`MARGINALIA_MMDC` (or `--mmdc`) names the binary when it is not on `PATH`. A
path that is wrong is an **error**, not a silent fallback to no pictures —
answering a typo with "install the thing you just said you had" would be worse.
`--diagrams=off` skips drawing entirely.

## Where sentence-level notes apply

Deliberately narrow: markdown prose. A tree node and a diagram statement are
already fine-grained, and inside a code fence the rendered text and the block's
plain text can differ — so the page simply does not offer there, and a note on
the whole block is what you get. That is better than an anchor nothing can
resolve.

## Code in a document

Fenced code blocks and `.proto` declarations are highlighted at render time, on
the **server**: comments recede, strings and numbers stand out. Go, Rust,
JS/TS, Python, shell, SQL, HCL, JSON, YAML, TOML, proto and HTTP are tokenized;
anything else renders as plain text rather than badly.

No highlighter is fetched — the page has to survive a strict CSP. That is also
why the mono stack only *prefers* JetBrains Mono, Fira Code, Cascadia Code or
Iosevka if the reviewer already has one, and falls back to the system mono.

## Multi-document sets

```bash
marginalia serve docs/                       # every supported file beneath it
marginalia serve spec.md plan.md api.proto   # or name them
```

A sidebar tree mirrors the folders the documents were found in, with a live
comment count and a tick per finished file. **Done with this file** marks one
document; **Finish review set** marks them all, appending `review_done` to every
log so an agent watching any one of them sees the handover close.

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
