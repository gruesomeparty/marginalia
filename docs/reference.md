# Reference

Every command also documents its flags in `marginalia <command> --help`.

## Commands

| | |
|---|---|
| `serve <doc\|dir>...` | Serve one or more documents for review |
| `export <doc>` | Write a self-contained review page for someone off your machine |
| `import <review.json>` | Merge a shared review back onto the document's log |
| `reply <doc>` | List the notes and their ids; `--to`/`--text` answers one |
| `addressed <doc>` | Record that you acted on a note, so the reviewer can check it |
| `suggestions <doc\|dir>...` | The `suggest_edit`s that are safe to apply verbatim |
| `review list` / `review show <name>` | Inspect the shipped framings |
| `mcp` | Speak MCP over stdio, or over Streamable HTTP with `--http` |
| `host` | Run one always-on instance serving every review in a store (hosted mode) |
| `version` | Print version information |

## Key flags

| Flag | On | Meaning |
|---|---|---|
| `--port` / `--host` | `serve` | Where to listen. Default `127.0.0.1:8787`; set the host to your Tailscale IP for remote review. |
| `--open` | `serve` | Open the page in a browser |
| `--watch` | `serve` | Re-parse when the file changes; the page offers a reload |
| `--until-done` | `serve` | Exit 0 once every served document is marked done |
| `--timeout <dur>` | `serve` | Stop after this long (`30m`) and exit 124 |
| `--config <yaml>` | `serve`, `export` | Framing, vocabulary, read-only blocks |
| `--review <name>` | `serve`, `export` | Start from a shipped framing |
| `--theme <name>` | `serve`, `export` | Palette the page arrives in |
| `--diagrams auto\|off` | `serve`, `export` | Draw mermaid diagrams |
| `--mmdc <path>` | `serve`, `export` | Where mermaid-cli lives |
| `--author <name>` | `serve`, `export`, `reply`, `addressed` | Recorded on events; defaults to `$USER` |
| `-o, --out` | `export` | Output file; default `<doc>.review.html` |
| `--doc <path>` | `import` | Which log to append to, when the file does not say |
| `--root <dir>` | `mcp` | Confine every path a tool call may reach |
| `--http <addr>` | `mcp` | Serve the tools over Streamable HTTP at `/mcp` instead of stdio; off loopback, `MARGINALIA_MCP_TOKEN` must be set |
| `--addr <addr>` | `host` | Where to listen; default `127.0.0.1:8080` |
| `--store <uri>` | `host` | Where review state lives: `file://<dir>` (default `file://./marginalia-data`) or `r2://<bucket>/<prefix>` |
| `--public-url <origin>` | `host` | The origin reviewers and agents reach the host at (scheme and host only); every URL handed out is built from it |
| `--human-jwt-issuer`, `--human-jwt-audience`, `--human-jwks-url` | `host` | How to verify the edge's JWT on page requests |
| `--reviewers <emails>` | `host` | The only humans the pages admit; default everyone the edge lets in |
| `--insecure-dev-identity <email>` | `host` | Every page request is this human; loopback only |
| `--agent-name <name>` | `host` | What agents holding the static token write as; default `agent` |
| `--mcp-oauth-issuer`, `--mcp-oauth-audience`, `--mcp-oauth-jwks-url` | `host` | Accept access tokens from an OAuth authorization server on `/mcp` (the claude.ai connector) |
| `--limit-*`, `--retain-done`, `--retain-open` | `host` | Caps and retention; defaults in [hosting.md](hosting.md#limits) |
| `--notify-url <url>`, `--notify-format text\|json` | `host` | POST a notification on agent activity (ntfy-ready text, or JSON) |
| `--json` | `suggestions`, `reply` | Machine-readable output |

## Environment

| | |
|---|---|
| `MARGINALIA_MCP_ROOT` | Default `--root` for `mcp` |
| `MARGINALIA_SERVE_TOKEN` | Opt-in token `serve` then requires on every request; notes are written under `--author` |
| `MARGINALIA_MCP_TOKEN` | Bearer token `mcp --http` and `host` require on `/mcp`; mandatory off loopback |
| `MARGINALIA_STORE` | Default `--store` for `host` |
| `MARGINALIA_PUBLIC_URL` | Default `--public-url` for `host` |
| `MARGINALIA_R2_ACCOUNT_ID`, `MARGINALIA_R2_ACCESS_KEY_ID`, `MARGINALIA_R2_SECRET_ACCESS_KEY` | Credentials for an `r2://` store; `MARGINALIA_R2_ENDPOINT` overrides the endpoint |
| `MARGINALIA_HUMAN_JWT_ISSUER`, `MARGINALIA_HUMAN_JWT_AUDIENCE`, `MARGINALIA_HUMAN_JWKS_URL` | Defaults for the `--human-jwt-*` flags of `host` |
| `MARGINALIA_REVIEWERS` | Default `--reviewers` for `host`, comma-separated |
| `MARGINALIA_AGENT_NAME` | Default `--agent-name` for `host` |
| `MARGINALIA_MCP_OAUTH_ISSUER`, `MARGINALIA_MCP_OAUTH_AUDIENCE`, `MARGINALIA_MCP_OAUTH_JWKS_URL` | Defaults for the `--mcp-oauth-*` flags of `host` |
| `MARGINALIA_LIMIT_*`, `MARGINALIA_RETAIN_DONE`, `MARGINALIA_RETAIN_OPEN` | Defaults for the `--limit-*` and `--retain-*` flags of `host` |
| `MARGINALIA_NOTIFY_URL` | Default `--notify-url`s for `host`, comma-separated |
| `MARGINALIA_MMDC` | Path to mermaid-cli |
| `MARGINALIA_MMDC_ARGS` | Extra arguments for it (a puppeteer config, say) |

## HTTP API

| | |
|---|---|
| `GET /` | The first document |
| `GET /d/{rel...}` | One page per document |
| `GET /api/doc[?doc=]` | The parsed document, the log, the resolution view and the review config |
| `GET /api/feedback[?doc=]` | The raw log |
| `POST /api/feedback[?doc=]` | Append one event (capped at 1 MB; 413 past it) |
| `GET /api/resolution[?doc=]` | The materialized view — per-block state, stale, orphaned |
| `GET /api/revision` | The re-parse counter, for `--watch` |
| `GET /api/wait?since=N[&timeout=S]` | Long-poll: answers once more than N events have been saved this run, or after S seconds (default 30, max 60). It counts what this server saved: a `reply` or `addressed` written through MCP goes to the log directly and does not wake it |
| `POST /api/session_done` | `review_done` on every document in the set |

A posted event names its own document, and the server only writes to documents
in the served set — a stale page must not be able to append elsewhere.

## Event schema

```jsonc
{
  "doc": "spec.md",
  "block": "5.3/2",          // section path + ordinal, or a node/statement path
  "quote": "The first ~90 characters of the block…",
  "hash": "9f2a1c4b9de0",    // sha256(normalized text)[:12] — how staleness is detected
  "type": "blocker",
  "text": "…",
  "fields": {"severity": "high"},
  "author": "berkay",
  "ts": "2026-09-16T08:11:15Z",
  "reply_to": "8f2a1c4b9de0", // on reply/addressed/confirm/reopen: the note this is about
  "sub": {                    // present only when the note is about one sentence
    "quote": "Retries use no backoff.",
    "prefix": "The cap is 500. ",
    "suffix": " Failures go to the log.",
    "start": 16,
    "hash": "c814be3e3d44"
  }
}
```

**Sub-anchors are a field, never a longer block id.** A sub-block id
(`5.3/2#1`, say) would be accepted by every consumer that already reads
`block`, and would silently mean something different to each of them. An event
with a `sub` still names its block exactly as before, so a reader that knows
nothing about this sees an ordinary note on the paragraph — which is where it
is, just less precisely.

Re-location is by **quote plus surrounding context**, the selectors the W3C
annotation model settled on for the same problem. An offset alone would always
"find" something, which is the failure mode worth avoiding: a note that
silently re-anchors to a neighbouring sentence is worse than one that admits it
is stale. So a sub-anchored note is judged by whether **its own sentence** is
still there, not by whether the paragraph changed — otherwise the feature would
be worthless on any paragraph anyone edits.

The page sends only the quote; the server derives the context and the offset
against its own copy of the block, so the two cannot disagree about text the
reviewer is looking at. A quote the block does not contain is refused rather
than stored as an anchor nothing could ever resolve.

**Types.** `comment`, `suggest_edit` (the `text` is the replacement),
`question`, `approve`, `reject`, plus any action the requesting agent
configured. The protocol's own events are `review_done`, `reply`, `addressed`,
`confirm` and `reopen` — a config can neither define nor disable those.

A note's **status** (`outstanding` / `addressed` / `confirmed` / `reopened`) and
its **staleness** are *derived* by replaying the log. Neither is a field anyone
writes, and nothing in the log is ever rewritten.

## Files on disk

| | |
|---|---|
| `<doc>.feedback.jsonl` | The log. Append-only, beside the document. |
| `.marginalia.yml` | Optional, in a served directory: whitelist, order, labels. |
| user cache dir | Drawn SVGs, keyed by source hash. Never beside your document. |
