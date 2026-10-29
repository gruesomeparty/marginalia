# Marginalia: remote hosting and MCP, where things stand

_Snapshot: 2026-10-29, `main` at `a732a2e`. Written to hand this work to another agent setup. It holds nothing account-specific: no hostnames, ids or secrets._

## TL;DR

- **Remote hosting is built.** Epic #91 (PRD §5.9, "Hosted mode") is on `main`:
  - `marginalia host` is one always-on instance that agents publish document content to over remote MCP.
  - The human reviews in a browser behind Cloudflare Access.
  - State lives in R2.
  - There is a Cloudflare Container deployment template in `deploy/cloudflare`.
- **It has never run on a real Cloudflare account.** The code, the image and the template are tested in CI, including a `wrangler deploy --dry-run` and a container smoke test. A live deploy is the next real step.
- **Do not deploy from v0.2.0.** The template and `MARGINALIA_PUBLIC_URL` support landed after that tag. The Worker passes settings only as environment variables, so a 0.2.0 container would hand out wrong URLs and OAuth would break. **Merge release PR #120 (0.3.0) first**, and merge PR #126 (container restart on settings change) before it.
- Three MCP setups exist. Which one fits depends on where the agent runs and where the reviewer is (see §3).

## 1. What exists today

### Three ways to run the MCP tools

| Mode | Command | Documents come from | Reviewer reaches | Auth |
|---|---|---|---|---|
| **Local stdio** (the original product) | `marginalia mcp [--root DIR] [--host IP]` | paths on the same disk (`--root` confines them) | a random port per review on `--host` (loopback, or a Tailscale IP) | process trust |
| **MCP over HTTP** (#93, PR #106) | `marginalia mcp --http :8080` + `MARGINALIA_MCP_TOKEN` | still paths on the server's disk | still one port per review | static bearer token (required off loopback) |
| **Hosted** (#91, PRs #105–#116, #119) | `marginalia host` | **content sent in the call** (`publish_document`) | one listener: `/` inbox, `/r/{id}/…` pages, `/mcp`, `/healthz` | edge JWT for humans; bearer token and/or OAuth for agents |

### Hosted mode in brief (details: `PRD.md` §5.9, `docs/hosting.md`, `deploy/cloudflare/README.md`)

- **Tools** (`internal/hosted/tools.go`): `publish_document`, `feedback_since`, `review_status`, `reply_to_note`, `mark_addressed`, `await_review_done`, `close_review`.
  - `publish_document` takes `{documents:[{name, content}], title, review, config, theme}` and returns a review URL.
  - Passing `review_id` publishes a new **revision**. Earlier revisions are kept, and existing notes re-anchor through `feedback.Materialize`, which marks them stale or orphaned.
- **Who may do what:**
  - Humans are authenticated by the edge's JWT (Cloudflare Access). The host verifies the signature, `iss`, `aud` and `exp` against the JWKS itself and never trusts the header blindly. The token's email becomes the author.
  - Agents authenticate on `/mcp` with a static token or an OAuth access token. The host is an RFC 9728 resource server, so it works as a **claude.ai custom connector** (#98).
  - Agents may append only `reply` and `addressed`. Humans append everything else, including `review_done`, `confirm` and `reopen`.
- **Storage** (`internal/storage`): two primitives.
  - Immutable blobs, written put-if-absent.
  - Append-only logs. There is no rewrite or delete in the API at all.
  - Backends: disk (the default; same bytes as the local `<doc>.feedback.jsonl`) and R2 (one object per event, conditional PUT).
  - The host is a **single writer**: it loads everything at startup, then writes through. Never run two instances on one bucket and prefix.
- **Cost model:**
  - One R2 PUT per comment.
  - Zero store reads per page view or wait.
  - One full listing per container start.
  - The container sleeps after 30 minutes idle. A waiting `await_review_done` keeps it awake.
- **Guard rails:** hard caps on what agents can publish. Each one has a flag and a `MARGINALIA_LIMIT_*` variable:
  - document size
  - documents per publish
  - revisions per review
  - publishes per hour
  - open reviews
  - total stored bytes
- **Retention:** finished reviews are *moved* under `archive/`, never deleted. A human's note is never refused by a budget.
- **Notifications** (#103): `MARGINALIA_NOTIFY_URL` takes an ntfy topic or webhook. It fires when an agent publishes, republishes, or replies after you finished. It sends titles and counts only, never content.
- **Image:** `ghcr.io/gruesomeparty/marginalia:<version>`, amd64 and arm64, distroless, non-root, `host --addr :8080`. Diagrams are off in hosted mode.

### Deployment template (`deploy/cloudflare`)

- The setup is a Worker in front of one Cloudflare Container (`max_instances: 1`, `sleepAfter = "30m"`), with R2 for state.
- Two Access applications are needed:
  1. One on the hostname, allowing the reviewer.
  2. One with a **Bypass** policy (Everyone) on `/mcp`, `/.well-known/oauth-protected-resource` and `/healthz`. Without it, agents get a login redirect instead of a 401.
- Settings live in a local `settings.env`, which git ignores. They are uploaded as Worker secrets with `npx wrangler deploy --domain <host> --secrets-file settings.env`.
- The `Dockerfile` is one line, `FROM ghcr.io/gruesomeparty/marginalia:<version>`. release-please bumps the tag.
- Account-specific material belongs in a **private deploy repo**, never in this one. PR #126 notes it was found "while wiring up the private deploy repo", so that repo has been started.

## 2. Open items blocking a first live deploy

| # | What | State | Why it matters |
|---|---|---|---|
| **PR #126** | `fix(deploy)`: restart the container when a deploy changes its settings | open, **CI green**, mergeable | Without it, rotating `MARGINALIA_MCP_TOKEN` doesn't reach a running container, and a waiting agent can keep the old one alive indefinitely |
| **PR #120** | release 0.3.0 (template + `MARGINALIA_PUBLIC_URL`) | open, `autorelease: pending`, mergeable state *unstable* (check CI) | The template README says to deploy from a release tag. v0.2.0 has no `deploy/` directory, and its binary doesn't read `MARGINALIA_PUBLIC_URL` |
| — | First real Cloudflare deploy | not done | Nothing has run on a real account: Access JWT verification, R2 conditional PUT (`TestR2Integration` runs only with credentials), cold-start timing, connector OAuth |
| PR #121 | typescript 5 → 7 in `deploy/cloudflare` (dependabot) | open | A major bump in the template. Merge only if `npm run check` passes; otherwise pin and close |
| PRs #122–#124, #127 | docker/* actions v4, codeql 4.39 | open | Routine. Merge when green |

Suggested order: merge #126 → let release-please refresh #120 → merge #120 (tags v0.3.0 and publishes the image) → deploy from `v0.3.0` in the private repo.

## 3. Options for your private setup

Pick by **where the agent runs** and **where you review**.

### Option A: Hosted on Cloudflare (the target design, recommended)

Use this when agents run anywhere: claude.ai Projects, Claude Code cloud sessions, CI or another laptop.

1. Private repo `marginalia-deploy` (or similar): a checkout of `deploy/cloudflare` from tag `v0.3.0`, plus a CI workflow that runs the same `wrangler deploy`. Keep `settings.env` contents in repo secrets, and `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` in the environment.
2. Cloudflare prerequisites: Workers Paid plan (Containers need it), a Zero Trust org (the free plan covers 50 users), the domain on the account, and an R2 bucket plus an API token scoped to that bucket.
3. Access: the reviewer app, plus the bypass app for the three agent paths.
4. Settings:
   - `MARGINALIA_PUBLIC_URL`
   - `MARGINALIA_STORE=r2://<bucket>/<prefix>`
   - the three `MARGINALIA_R2_*` values
   - `MARGINALIA_HUMAN_JWT_ISSUER` and `MARGINALIA_HUMAN_JWT_AUDIENCE`
   - `MARGINALIA_MCP_TOKEN` (`openssl rand -hex 32`)
   - optionally `MARGINALIA_MCP_OAUTH_ISSUER` and `MARGINALIA_NOTIFY_URL`
5. Connect agents:
   - **Claude Code (local or cloud):** add to `.mcp.json`:
     ```jsonc
     { "mcpServers": { "marginalia": { "type": "http",
         "url": "https://<host>/mcp",
         "headers": { "Authorization": "Bearer ${MARGINALIA_MCP_TOKEN}" } } } }
     ```
     In a cloud environment, put the token in the environment's secrets and make sure its network policy allows `<host>`.
   - **claude.ai (Projects/chat):** add a custom connector to `https://<host>/mcp`, with OAuth through a Cloudflare **Access for SaaS (OIDC)** app (`docs/hosting.md`, "Wiring Cloudflare Access as the authorization server").
6. Smoke test:
   - `curl https://<host>/healthz` returns `ok`.
   - Opening `/` goes through Access to the inbox.
   - An agent calls `publish_document`, you comment and press Done, and `await_review_done` returns.

Costs: Workers Paid base fee, plus container time while awake, plus roughly one R2 PUT per note. Idle costs nothing.

### Option B: Self-host the image somewhere else (VPS, home server, Fly, …)

The binary is cloud-neutral. Run `docker run … -e MARGINALIA_STORE=file:///data -v data:/data` behind anything that issues a verifiable JWT on page requests (any OIDC-aware proxy: set the issuer, audience and JWKS URL). Agents still use `/mcp` with a token.

- **Pros:** no Cloudflare dependency.
- **Cons:** you own TLS, the edge identity provider and backups. The PRD's target and this repo's template are Cloudflare-only, so you would be off the documented path.

### Option C: `marginalia mcp --http` on a tailnet machine

This is the simplest remote option: one process and one token. It still works on **paths**, so the documents must be on that machine. It also opens one port per review, which works on a tailnet but not behind a proxy.

Fits if your agents run on that same box. It **does not** fit claude.ai Projects or cloud sessions, which can't reach a tailnet.

### Option D: Local stdio (unchanged)

This is the default product: `{ "command": "marginalia", "args": ["mcp"] }`. Use it when the agent and the reviewer share a machine or a tailnet (`--host <tailscale-ip>` for phone review).

### Decision guide

| Agent runs in… | Reviewer on… | Use |
|---|---|---|
| Local Claude Code, same machine | laptop / phone via tailnet | D |
| One server you also review from | tailnet | C (or D over `ssh host marginalia mcp`) |
| claude.ai Projects, Claude Code cloud, CI, several machines | anywhere | **A** (or B) |

## 4. Open backlog (all issues, none hosted-specific)

| # | Title | Note |
|---|---|---|
| 90 | Reviewers cannot reply to requester notes directly | agent-feedback |
| 87 | Evaluate mermaid-cli 12.0.0 | testdata is pinned to the rendered version (#86) |
| 60 | The page's JavaScript has no tests | |
| 55 | The agent's half of `suggest_edit` | |
| 54 | Auth and provenance before a review crosses a trust boundary | **Largely answered by hosted mode's identity split.** Review it and close it or narrow it |
| 53 | More than one reviewer on the same document | Hosted mode assigns authors from JWTs, so this is now cheaper |
| 52 | More inputs: OpenAPI, HCL, SQL migrations (diffs done) | |
| 50 | Accessibility and touch on drawn diagrams | Matters more for phone review against a hosted instance |
| 44 | Blocking handoff: how an agent waits | `await_review_done` exists on both surfaces. Probably closable; check the acceptance criteria |
| 31 | Source files as reviewable input | |

Housekeeping:
- Merged epic branches are still on the remote: `claude/{host-95,storage-94,mcp-http-93,identity-97,publish-96,oauth-98,inbox-99,limits-100,r2-101,image-102,notify-103,prd-hosted-92}` and `feature/hosted`. Delete them once their PRs are confirmed merged.
- `claude/cloudflare-template` carries PR #126. Keep it.

## 5. Rules the next agent must keep (from `CLAUDE.md` and the PRD)

- **Never mutate the source document.** In hosted mode, snapshots are immutable and a republish appends a revision.
- **Feedback is append-only.** Storage structurally has no rewrite or delete. Archiving *moves* a prefix.
- **An agent may answer and claim, never decide.** Agent credentials append only `reply` and `addressed`. Never add an agent-side `confirm`, `reopen`, `comment` or `review_done`.
- **One instance per store**, always.
- **Nothing account-specific in this repo.** Hostnames, ids and secrets live in the private deploy repo or in secrets. Credentials are read only from the environment, never from flags.
- **Stdout belongs to the protocol** in stdio `mcp`.
- **No database. No Clipboard API. Self-contained HTML that survives strict CSP.**
- Pipeline: one issue → one PR based on `main` → stop. Never merge or approve as an agent. Issues labelled `base:feature/<name>` target an integration branch, and `integration-automerge.yml` merges those on green CI. Match CI's pinned `golangci-lint`.

## 6. Commands

```bash
go build ./... && go test -race ./...
golangci-lint run
go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80
# hosted, on a laptop with no edge (loopback only):
go run . host --insecure-dev-identity you@example.com
# template checks
cd deploy/cloudflare && npm ci && npm run check && npx wrangler deploy --dry-run --containers-rollout=none
```

## 7. Kickoff prompt for the new setup

> You're continuing work on Marginalia (`gruesomeparty/marginalia`). Read `CLAUDE.md`, `PRD.md` §5.9, `docs/hosting.md` and `HANDOFF-hosted-mcp.md` first. Goal: get the first live hosted instance running from my private deploy repo.
>
> Steps:
> 1. Check PR #126 and release PR #120. Tell me what is needed to merge them. I merge, not you.
> 2. Once v0.3.0 is tagged and its GHCR image exists, set up the private deploy repo from `deploy/cloudflare` at `v0.3.0`, with a CI deploy workflow. Secrets come from repo secrets, and no account values get committed.
> 3. Walk me through the Cloudflare-side steps I must do by hand: Access apps, the R2 token, and Access for SaaS for the connector.
> 4. Run the smoke test and report exactly what passed.
>
> Don't change Marginalia's invariants. Anything account-specific stays out of the public repo.
