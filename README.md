# Marginalia

**A document-review tool an agent hands to a human.**

Marginalia renders a document as a readable page, lets a person comment on any
block of it, and writes every comment back as an append-only JSONL event the
agent reads directly. Render, annotate, return events. No database, no export
step: feedback lands beside the document as `<doc>.feedback.jsonl` the instant
it is saved.

```
agent ──serve──▶ page ──human clicks a block──▶ <doc>.feedback.jsonl ──▶ agent
  ▲                                                                       │
  └──────────────── answers, revises, says what changed ──────────────────┘
```

The [homepage](https://marginalia.gruesomeparty.xyz) has a live review page to
click around in.

## Why

When an agent writes a spec, a schema or a plan, the human's review usually
comes back as one chat message: "looks fine, but the retry bit is wrong".
The agent has to guess which retry bit, and the next round starts from a full
re-read. Marginalia gives the review a shape both sides can use. The person
clicks the paragraph, the field or the arrow in a diagram and says what they
think. The agent gets an event that names that exact block, notices when the
text it was about has since changed, and can answer or say what it fixed, in
place.

## What it reads

Markdown, `.proto`, JSON / YAML / TOML (OpenAPI specs by operation), mermaid
diagrams (drawn and clickable when mermaid-cli is installed), unified diffs,
SQL and HCL. One file, several, or a whole directory. Everything but markdown
renders as a folding tree anchored by its own path, so a note on
`CreateOrderRequest/customer_id` stays on that field. See
[supported formats](docs/formats.md).

## Install

```bash
brew install --cask gruesomeparty/tap/marginalia   # macOS
go install github.com/gruesomeparty/marginalia@latest
```

Or download a build from the
[releases page](https://github.com/gruesomeparty/marginalia/releases) (Linux
and macOS, amd64 and arm64). It is a single Go binary with no runtime
dependencies.

As a Claude Code plugin, which also wires up the MCP server:

```
/plugin marketplace add gruesomeparty/marginalia
/plugin install marginalia
```

Then `/marginalia:review-doc <path>` hands a document to a human and reads the
answers back.

## Quickstart

```bash
marginalia serve README.md --open
```

Click any block to comment, press **Done**, then look at
`README.md.feedback.jsonl`.

From an agent, prefer the MCP server (`marginalia mcp`): it hands the document
over, waits for the reviewer, and reads, answers and resolves notes without
parsing anything. [Using it from an agent](docs/agents.md) covers the setup and
the whole review loop.

## Highlights

- **Anchors that notice change.** Every block carries an id, a quote and a
  hash. A note written against text that has since changed comes back marked
  *stale* instead of silently pointing at the wrong thing.
- **Reviews framed by the agent.** Instructions, a vocabulary (`blocker`,
  `nit`, …), structured fields and off-limits sections, enforced by the server.
  Four framings ship built in: `adr`, `schema`, `security`, `copy`.
- **A revision loop.** The agent answers questions in a thread and records
  which note a change addressed. The reviewer confirms or reopens. An agent
  can answer and claim, never decide.
- **A page people finish.** One-tap verdicts, sentence-level notes, keyboard
  movement between unanswered blocks, an outline with progress, and a phone
  layout.
- **Three ways to share.** `serve` on your machine or tailnet,
  [`export`](docs/sharing.md) to a single self-contained HTML file, or
  [hosted mode](docs/hosting.md) for agents that share no filesystem with you.

## Documentation

| | |
|---|---|
| [Using it from an agent](docs/agents.md) | MCP setup, the tools, the CLI, and the review loop step by step |
| [Reviewing a document](docs/reviewing.md) | The page from the reviewer's side: verdicts, keys, filters, Done |
| [Framing a review](docs/review-config.md) | Built-in framings and writing your own `review.yaml` |
| [Supported formats](docs/formats.md) | How each input is anchored, drawn diagrams, multi-document sets |
| [Sharing off your machine](docs/sharing.md) | `export` and `import` |
| [Hosted mode](docs/hosting.md) | `marginalia host`: publishing over remote MCP, the inbox, running it |
| [Reference](docs/reference.md) | Commands, flags, environment, HTTP API, event schema |
| [Design and limits](docs/design.md) | The rules the design is built on, and what it does not do yet |

[`PRD.md`](PRD.md) is the full product spec.

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md).
Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md),
which also explains how to verify a release.

## License

[MIT](LICENSE)
