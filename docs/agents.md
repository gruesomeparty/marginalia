# Using Marginalia from an agent

Agents are Marginalia's primary users. There are two ways in: the MCP server,
which is the better one, and the CLI, which does the same things from a shell.
An agent on another machine with no shared filesystem publishes to
[hosted mode](hosting.md) instead.

## MCP (preferred)

**Installed as a Claude plugin, this is already done** — the plugin declares the
server, so the tools are there as soon as the `marginalia` binary is on `PATH`.
If the tools are missing, that binary is the thing to check: a server that
cannot start looks exactly like one nobody configured.

For any other MCP client:

```jsonc
// in your MCP client's config
{ "mcpServers": { "marginalia": { "command": "marginalia", "args": ["mcp"] } } }
```

Add `"--root", "<dir>"` to confine it somewhere other than the working
directory; `MARGINALIA_MCP_ROOT` does the same without a flag.

**From another machine** — a Claude Code session on your laptop driving a
review server on the box where the documents live, say, over Tailscale — the
same tools speak Streamable HTTP:

```bash
export MARGINALIA_MCP_TOKEN="$(openssl rand -hex 32)"   # required off loopback
marginalia mcp --http 100.64.0.9:8080 --host 100.64.0.9 --root ~/docs
```

`--http` is where the MCP endpoint listens (`/mcp`, with `/healthz` beside
it for health checks); `--host` is still where the review pages bind, so the
URL a tool hands back is one your phone can open. Without
`MARGINALIA_MCP_TOKEN` the command refuses to bind anything but a loopback
address: an unauthenticated MCP endpoint on a reachable interface is not an
option. The client sends the token as a bearer:

```jsonc
// .mcp.json, or any client that takes an HTTP server
{ "mcpServers": { "marginalia": {
    "type": "http",
    "url": "http://100.64.0.9:8080/mcp",
    "headers": { "Authorization": "Bearer ${MARGINALIA_MCP_TOKEN}" } } } }
```

Stdout stays clean in this mode too — notices go to stderr — and SIGINT or
SIGTERM drains in-flight calls before the process exits, as `serve` does.

### The tools

| Tool | What it does |
|---|---|
| `review_document` | Serve a document (or a set); returns the URL to hand over and the vocabulary you will read back. |
| `feedback_since` | Only what is new, by cursor. Every event carries the `note_id` you would answer it with. |
| `review_status` | The log materialized against the document as it now reads: per-block state, what went stale, what is orphaned, and which `suggest_edit`s are safe to apply verbatim. |
| `reply_to_note` | Answer one of the human's notes in place. |
| `mark_addressed` | Record that you changed the document in answer to a note. |
| `await_review_done` | Wait until they finish or ask a question (`reason: "question"`, so you can answer before Done). Short by default (45s, under common client tool timeouts): call it in a loop with the returned cursor. A timeout is not an error — it returns `done: false`. |
| `close_review` | Stop serving. Comments already saved stay on disk and stay readable. |

Three things about it are deliberate:

- **Reads come from the log on disk**, never from the running HTTP server. They
  keep working after your session ends, and any number of agents can follow one
  review by passing `paths` instead of a `session_id`.
- **An agent may answer and may claim, never decide.** `reply_to_note` and
  `mark_addressed` are the only tools that write, each writes one fixed type,
  and both name the note they are about. There is deliberately no confirm or
  reopen tool: that is the reviewer's verdict on your work, and an agent that
  could settle its own note would make the revision loop decorative. Everything
  else in the log is theirs — `review_done` above all, since it is the one
  signal the handover exists to produce.
- **`--root` confines every path a tool names.** A tool argument can come from
  text the model read, and `.yaml` is a supported input, so without a root
  "review `~/.config/gh/hosts.yml`" would render a token onto a listening
  socket. The check runs before anything is parsed or served.

## CLI

Everything the MCP server does, you can do with the binary:

```bash
marginalia serve spec.md --review schema      # hand it over
marginalia reply spec.md                      # what they said, and the ids
marginalia reply spec.md --to <id> --text …   # answer a question
marginalia addressed spec.md --to <id> --text … # say what you changed
marginalia suggestions spec.md --json         # what is safe to apply
```

`serve` prints the URL and the log path on startup, then runs until
interrupted. Feedback is readable from disk the whole time — you do not need to
stop the server to read it.

## The review loop

### 1. Hand it over

```bash
marginalia serve spec.md --review schema --watch
```

`--watch` re-reads and re-parses when the file changes, so an edit lands without
a restart. The page never reloads under the reviewer's hands: a change while a
comment is half-typed shows a *"this document changed on disk — Reload"* prompt
and auto-reloads only when nothing is open. Watching polls size and mtime a
couple of times a second rather than using filesystem notifications, which miss
the temp-file-and-rename that editors save with.

### 2. Read what they said

`review_status` over MCP, or straight from the server:

```bash
curl -s localhost:8787/api/resolution | jq '.outstanding, .stale, .orphaned'
```

The resolution view is the log replayed against the document **as it now
reads** — not the raw log:

- a note whose block still reads the same **stands**, and the badge shows what
  was decided;
- a note written before that block changed is **stale**, with the text it was
  written against (`was: "…"`) so you can see what moved;
- a note whose block is gone entirely is listed as **no longer anchored** at the
  end of the page instead of disappearing.

Nothing is rewritten to produce it. Staleness is a reading of the log, not a
fact on disk.

### 3. Answer their questions

A question used to be a dead end — you could revise the document and hope they
noticed.

```bash
marginalia reply spec.md                         # the notes, and the ids
marginalia reply spec.md --to 8f2a1c4b9de0 --text "The cap comes from the upstream API."
```

The answer hangs under their own note the next time the page renders, and the
page shows a **Reply** box under every note so they can answer back. A reply
changes nothing about the note it answers: the question stays the block's
current state, and the header count still says how much the *reviewer* said.
Threads are one level deep on purpose — an answer to an answer re-roots to the
note that started it.

Note ids are **derived** from the event, so every log already on disk has them
and an id survives `export`/`import` unchanged.

### 4. Revise, and say which note you were answering

```bash
marginalia addressed spec.md --to 8f2a1c4b9de0 --text "Raised the cap to 2000."
```

Their next look shows that block as **addressed — is this right now?**, with
their original note beside what the block now says, and two buttons: *Yes,
settled* or *No, still open*. The second round is a short queue instead of a
full re-read.

**The claim is checkable, not just asserted.** If a re-render finds that the
thing the note is about has not moved, the page says so next to the claim — the
edit may be somewhere else, or may not have happened. The claim is still shown;
this is a review tool, not a court.

What gets checked is what the note is about: the block's hash for a block-level
note, and the *sentence* for a sentence-level one. So editing a different
sentence in the same paragraph does not make an unrelated claim look borne out.

A note nobody addressed stays **outstanding**, however much the document moves
around it: silence is not resolution. An `approve` is not outstanding work.
Confirming and reopening are the reviewer's, and there is no tool for them.

### 5. Apply what is safe

```bash
marginalia suggestions spec.md --json
```

A `suggest_edit` is **applicable** only when it is the note that stands for its
block, its hash still matches the block as the document now reads, *and* it
carries a hash at all. Everything else comes back under `needs_confirmation`
with the reason — the block changed since, a later note supersedes it, the block
is gone, or there is no hash to check.

Marginalia never edits your document. This reports; you apply.
