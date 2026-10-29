# Deploying marginalia host to Cloudflare

This directory is a complete, credential-free deployment of
[hosted mode](../../docs/hosting.md): a Worker on your domain, in front of one
**Cloudflare Container** running the released image, with every review in an
**R2** bucket and **Cloudflare Access** in front of the pages. Nothing in it
names an account, a domain or a secret. Those are yours: the hostname goes on
the deploy command line, and the settings go in a file that stays on your
machine.

```
browser ──▶ Access ──▶ Worker ──▶ Container: marginalia host ──▶ R2
agent ────────────────▶ /mcp (bearer token or OAuth, checked by the host)
```

What the files do:

| File | Purpose |
|---|---|
| `wrangler.jsonc` | One Worker, one Container class, `max_instances: 1`, no `workers.dev` or preview URL |
| `Dockerfile` | `FROM ghcr.io/gruesomeparty/marginalia:<version>`. Each release moves the tag. |
| `src/index.ts` | Forwards every request to the single container and hands it every `MARGINALIA_*` setting. Refuses with a 500 when the public URL or an R2 store is missing, and answers an `/mcp` request with no valid credential itself, and an anonymous `/healthz` from the container's state, so a stranger cannot wake (and bill) the container. |
| `settings.example.env` | Every setting, documented. Copy it to `settings.env`, which git ignores. |

## You need

- A Cloudflare account on the **Workers Paid** plan, which Containers
  require, with your domain on it.
- A **Zero Trust** organization for Access. The free plan covers 50 users.
- **Node 20+** and a running **Docker**. Wrangler builds the one-line
  Dockerfile locally and pushes the image to Cloudflare's registry, which
  is the only registry a Container can pull from.

## 1. Take the template from a release

```bash
git clone --depth 1 --branch v<version> https://github.com/gruesomeparty/marginalia
cd marginalia/deploy/cloudflare
npm ci
npx wrangler login
```

Use a release tag, not `main`. The `Dockerfile` names the image published for
that release, so the Worker and the binary always come from the same version.

## 2. The bucket

```bash
npx wrangler r2 bucket create marginalia-reviews
```

Then, in the dashboard under **R2 → Manage API tokens**, create a token with
**Object Read & Write** on that bucket only. Its access key id and secret,
and the account id from the R2 overview page, are the three
`MARGINALIA_R2_*` settings.

## 3. Access

Make both applications in **Zero Trust → Access → Applications**, both of
type *self-hosted*:

1. **The reviewer.** Create an application on `review.example.com` (your
   hostname) with an *Allow* policy for your email or group. Note its
   **AUD tag**, which goes in `MARGINALIA_HUMAN_JWT_AUDIENCE`, and your team
   domain, which goes in `MARGINALIA_HUMAN_JWT_ISSUER`.
2. **The agents' door.** Create a second application covering
   `review.example.com/mcp`,
   `review.example.com/.well-known/oauth-protected-resource` and
   `review.example.com/healthz`, with a single policy whose action is
   **Bypass** and which includes *Everyone*.

   This one is not optional. An agent has no browser to log in with. Behind
   the first application, `/mcp` would answer every agent, the claude.ai
   connector included, with a redirect to a login page. The host
   authenticates these paths itself: `/mcp` needs the bearer token or an
   OAuth token, the metadata document is public by design, and `/healthz`
   only says `ok`. The more specific path wins in Access, so everything else
   stays behind the reviewer's policy.

Access is the reviewer's front door, but it is not the host's only check.
The host verifies Access's JWT on every page request itself, so even a
request that went around Access gets nothing.

## 4. Settings

```bash
cp settings.example.env settings.env
openssl rand -hex 32   # MARGINALIA_MCP_TOKEN
$EDITOR settings.env
```

Fill in everything under *Required*. To let the **claude.ai connector**
connect, add the OAuth issuer as described in
[docs/hosting.md](../../docs/hosting.md#agents-mcp-as-an-oauth-resource-server).

## 5. Deploy

```bash
npx wrangler deploy --domain review.example.com --secrets-file settings.env
```

This one command does all of the following:

- builds and pushes the image
- creates the DNS record and certificate for the hostname
- deploys the Worker
- uploads every line of `settings.env` as an encrypted secret

The first Container can take a few minutes to provision. Until it is ready,
requests answer 503.

Check it:

```bash
curl https://review.example.com/healthz   # ok, or "ok (asleep)": answered by the Worker
```

Opening `https://review.example.com/` should take you through Access to the
inbox. Point an agent at it with the token, for example in Claude Code's
`.mcp.json`:

```jsonc
{ "mcpServers": { "marginalia": {
    "type": "http",
    "url": "https://review.example.com/mcp",
    "headers": { "Authorization": "Bearer ${MARGINALIA_MCP_TOKEN}" } } } }
```

## Running it

- **It sleeps.** After `sleepAfter` (30 minutes) with no request in flight,
  the container stops and costs nothing; the next request starts it again.
  An agent waiting in `await_review_done` keeps it awake. All state is in R2,
  so sleeping loses nothing except the time the next start takes: the host
  loads every open review before it answers. That can take a while on a
  large store, so the Worker allows two minutes for it. If you would rather
  never wait, raise `sleepAfter` in `src/index.ts`, and pay for the hours.
- **One instance, always.** The host is its store's single writer: it loads
  everything at startup and keeps it in memory. `max_instances` stays `1`.
  Never point a second deployment at the same bucket and prefix.
- **Changing a setting.** Edit `settings.env` and deploy again. The binary
  reads its settings once, when it starts. So the Worker remembers a hash of
  the settings the running container started with. On the first request
  after a deploy that changed them, it stops the container and starts it
  again. The stop is a SIGTERM, so the host finishes in-flight requests
  first. A rotated token takes effect on that request, without waiting for
  the container to sleep. The file is applied additively, so removing a
  setting takes `npx wrangler secret delete <NAME>`, and then a deploy.
- **Upgrading.** Check out the next release tag and run `npm ci`, then deploy
  with the same command. The new `Dockerfile` names the new image.
- **Logs.** `npx wrangler tail` streams the Worker's logs. The container's
  own output, where the host says what it loaded and what it refused, is
  under *Containers* in the dashboard. If the Worker answers `the host did
  not start`, that log names the setting the binary refused.
- **From CI instead of a laptop.** Run the same command in a private
  repository's workflow. Put the settings file's contents in that
  repository's secrets, and `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`
  in the environment. Keep this repository free of anything account-specific.
