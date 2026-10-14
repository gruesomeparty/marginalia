# Hosting Marginalia

`marginalia host` is one always-on instance that agents on other machines
publish documents to over remote MCP and the owner reviews from a browser
(PRD §5.9). This page is the operator's side of it: what the binary expects
from the world around it, written as steps for a deploy repository. Nothing
here is account-specific; every value is a placeholder.

The binary ships here. Infrastructure — the container, the Worker in front of
it, the bucket, the DNS, the Access applications, the secrets — belongs in a
separate, private deploy repository.

## What the instance expects

- **One instance per store.** The process loads every review from `--store`
  at startup, keeps them in memory and writes through. Two instances on one
  store would each believe they were the only writer. Pin the container to
  exactly one instance.
- **An edge that authenticates humans.** Page routes (`/`, `/r/<id>/…`)
  require a JWT from the edge on every request, verified against the
  issuer's published keys. The container's port may be reachable around the
  edge, so the header alone is never trusted.
- **A credential for agents on `/mcp`.** A static bearer token
  (`MARGINALIA_MCP_TOKEN`), an OAuth authorization server, or both.
- **Secrets from the environment only.** No credential rides on a command
  line: flags show up in process lists and logs.

| Variable | What it is |
|---|---|
| `MARGINALIA_STORE` | `file://<dir>` (R2 arrives with the next storage backend) |
| `MARGINALIA_MCP_TOKEN` | Static bearer token for `/mcp`; mandatory off loopback unless OAuth is configured |
| `MARGINALIA_HUMAN_JWT_ISSUER` | Issuer of the edge's JWT, e.g. `https://<team>.cloudflareaccess.com` |
| `MARGINALIA_HUMAN_JWT_AUDIENCE` | Audience the edge's JWT must carry, e.g. the Access application's AUD tag |
| `MARGINALIA_HUMAN_JWKS_URL` | Where the issuer publishes its keys; defaults to the Access certs URL, or OpenID discovery |
| `MARGINALIA_MCP_OAUTH_ISSUER` | OAuth authorization server for `/mcp` (the claude.ai connector) |
| `MARGINALIA_MCP_OAUTH_AUDIENCE` | Audience its access tokens must carry; defaults to `<public-url>/mcp` |
| `MARGINALIA_MCP_OAUTH_JWKS_URL` | Where it publishes its keys; defaults to OpenID discovery |
| `MARGINALIA_AGENT_NAME` | What agents holding the static token write as; default `agent` |

And the flags that are not secrets: `--addr` (`:8080` in a container),
`--public-url https://review.<your-domain>` (every URL handed out is built
from it; required for OAuth, since it names the protected resource).

## Humans: Cloudflare Access in front of the pages

1. In the deploy repo, put an **Access application** (self-hosted) on the
   hostname the instance is served at, with a policy allowing the reviewer
   (an email, a group). Access then authenticates every browser request and
   forwards it with a `Cf-Access-Jwt-Assertion` header (and sets a
   `CF_Authorization` cookie).
2. Note the application's **AUD tag** and the team domain.
3. Configure the instance:

   ```
   MARGINALIA_HUMAN_JWT_ISSUER=https://<team>.cloudflareaccess.com
   MARGINALIA_HUMAN_JWT_AUDIENCE=<the application's AUD tag>
   ```

   The keys are fetched from
   `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, cached, and
   refreshed when a token names a key the cache does not hold.
4. The Worker in front of the container must **forward the
   `Cf-Access-Jwt-Assertion` header** to it. The instance verifies the
   signature, issuer, audience and validity itself; the token's `email`
   becomes the author of everything that reviewer writes.

Any other identity provider works the same way: set the issuer and audience
to what it puts in its tokens, and the key-set URL if the issuer does not
publish an OpenID configuration at `/.well-known/openid-configuration`.

Bypassing the edge on a laptop: `--insecure-dev-identity you@example.com`
makes every page request that human. It refuses to start on anything but a
loopback address.

## Agents: `/mcp` as an OAuth resource server

Marginalia never issues a token, stores a user or shows a login page. It is a
**resource server** (RFC 9728): an unauthenticated request to `/mcp` is
answered with

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer resource_metadata="https://review.<your-domain>/.well-known/oauth-protected-resource/mcp"
```

and that document names the authorization server:

```json
{ "resource": "https://review.<your-domain>/mcp",
  "authorization_servers": ["https://<authorization-server>"],
  "bearer_methods_supported": ["header"] }
```

A client such as claude.ai reads it, runs the authorization-code flow (with
PKCE) against that server, and presents the access token it gets as a
bearer on every `/mcp` request. The instance verifies that token exactly as
it verifies the edge's: signature against the server's published keys,
`iss`, `aud`, `exp`. The token's subject (or `email`, when present) becomes
the agent's name, prefixed `agent:`. **Whatever the token claims, it is an
agent's seat**: it opens the tools that answer and claim, never one that
writes a `comment`, a verdict, a `confirm`, a `reopen` or a `review_done`,
and it cannot open the review pages.

### What the authorization server has to do

- Issue **JWT access tokens** signed with an asymmetric key (RS256, ES256 or
  EdDSA) and publish the public keys as a JWKS, either at the `jwks_uri` of
  its OpenID configuration (`<issuer>/.well-known/openid-configuration`) or
  at a URL you pass in `MARGINALIA_MCP_OAUTH_JWKS_URL`.
- Put the resource — `https://review.<your-domain>/mcp` — in the token's
  `aud`, and its own issuer URL in `iss`.
- Support the authorization-code flow with PKCE for a public client, and
  either dynamic client registration or a pre-registered client whose id (and
  secret, if any) you enter when adding the connector.

### Wiring Cloudflare Access as the authorization server

Cloudflare Access can act as an OpenID Connect provider for a SaaS
application, which is what the connector is to it:

1. Create an **Access for SaaS** application of type OIDC, with a policy
   allowing the identities that may run agents (yourself, a service
   account).
2. Set its redirect URL to the connector's callback — claude.ai shows it
   when you add a custom connector — and note the client id, the client
   secret, and the issuer it publishes, of the form
   `https://<team>.cloudflareaccess.com/cdn-cgi/access/sso/oidc/<client-id>`.
3. Configure the instance:

   ```
   MARGINALIA_MCP_OAUTH_ISSUER=https://<team>.cloudflareaccess.com/cdn-cgi/access/sso/oidc/<client-id>
   ```

   The audience defaults to `<public-url>/mcp` and the keys are found through
   the issuer's OpenID configuration. If the provider puts something else in
   `aud` (some put the client id), set `MARGINALIA_MCP_OAUTH_AUDIENCE` to
   that.

Any OpenID provider that meets the list above (Auth0, Okta, Keycloak, …) fits
the same three steps.

### Adding it as a claude.ai connector

1. In claude.ai, add a custom connector with the URL
   `https://review.<your-domain>/mcp`.
2. If the authorization server does not register clients dynamically, enter
   the client id and secret from the step above.
3. Approve the consent screen. From then on an agent in any chat with the
   connector enabled has `publish_document` and the other tools, and
   everything it writes is logged as `agent:<its subject>`.

### Claude Code and scripts: the static token

The static token keeps working beside OAuth, for Claude Code's `.mcp.json`
and for scripts:

```jsonc
{ "mcpServers": { "marginalia": {
    "type": "http",
    "url": "https://review.<your-domain>/mcp",
    "headers": { "Authorization": "Bearer ${MARGINALIA_MCP_TOKEN}" } } } }
```

Set `MARGINALIA_AGENT_NAME` to tell those agents apart from the connector's
in the logs.
