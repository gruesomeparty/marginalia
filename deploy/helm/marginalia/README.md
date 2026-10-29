# Marginalia Helm chart

Runs Marginalia's hosted mode (`marginalia host`) on any Kubernetes cluster:
one pod, its store on a PersistentVolumeClaim, an OIDC proxy you already run
in front of the pages, and Ingress or Gateway API routes. What hosted mode
is, and what it expects of a deployment, is in
[docs/hosting.md](../../../docs/hosting.md).

```sh
kubectl create namespace marginalia
kubectl -n marginalia create secret generic marginalia \
  --from-literal=MARGINALIA_MCP_TOKEN="$(openssl rand -hex 32)"

helm install marginalia ./deploy/helm/marginalia -n marginalia \
  --set publicURL=https://review.example.com \
  --set auth.human.issuer=https://idp.example.com \
  --set auth.human.audience=<oidc-client-id> \
  --set existingSecret=marginalia \
  --set ingress.enabled=true --set ingress.className=nginx
```

The chart refuses to render without `publicURL`, an issuer and audience for
reviewers, and a credential for agents, rather than leaving the pod to
crash-loop on the same complaint.

## What it does and does not let you change

- **One replica, always.** The host loads every review at startup and is its
  store's single writer. The Deployment has `replicas: 1` and the
  `Recreate` strategy, so an upgrade stops the old pod before the new one
  opens the store. There is no replica or autoscaling setting.
- **State on a volume.** `persistence` creates a `ReadWriteOnce` claim (or
  uses `existingClaim`) mounted at `/data`, and the store is
  `file:///data`. The claim is kept when the release is uninstalled
  (`persistence.retain`). `store` overrides the URI for a remote backend.
- **Credentials from a Secret you own.** `existingSecret` is loaded whole
  into the environment: `MARGINALIA_MCP_TOKEN`, and anything else private.
  The chart takes no credential as a value, so none ends up in a release
  manifest. External Secrets or Sealed Secrets produce the same Secret.
- **A locked-down pod.** Non-root (65532, the image's user), read-only root
  filesystem, every capability dropped, `RuntimeDefault` seccomp, no service
  account token. Optional NetworkPolicy for who may reach the port.
- **Settings restart the pod.** The host reads its environment once, so the
  pod template carries a checksum of it. A Secret changed in place needs a
  `kubectl rollout restart`.

## Reviewers: an OIDC proxy in front of the pages

The proxy signs reviewers in and forwards their **ID token** on every
request. The host verifies it against the issuer's published keys
(signature, issuer, audience, expiry), so a header that did not come from the
proxy gets nowhere, and the token's `email` (else `sub`) is the author of
everything that reviewer writes.

| Value | |
|---|---|
| `auth.human.issuer` | The IdP's issuer URL, as it appears in the token's `iss` |
| `auth.human.audience` | The token's `aud`: for an ID token, the OIDC client id |
| `auth.human.jwksURL` | Only if the issuer has no `/.well-known/openid-configuration` |
| `auth.human.header` | The header the proxy forwards it in (default `X-Auth-Request-Id-Token`) |
| `auth.human.cookie` | A cookie holding it, if the proxy uses one (default: none) |

The header cannot be `Authorization`. On a page route a bearer token is an
agent's credential and is refused outright, so the proxy has to put the
reviewer's token in a header of its own.

Discovery runs at startup, so the pod needs to reach the issuer to start.

### oauth2-proxy with ingress-nginx

Run oauth2-proxy as an auth service for the ingress, and have it return the
ID token in its auth response. With its alpha config:

```yaml
injectResponseHeaders:
  - name: X-Auth-Request-Id-Token
    values:
      - claimSource:
          claim: id_token
```

Then point the pages Ingress at it:

```yaml
ingress:
  enabled: true
  className: nginx
  pagesAnnotations:
    nginx.ingress.kubernetes.io/auth-url: http://oauth2-proxy.auth.svc.cluster.local/oauth2/auth
    nginx.ingress.kubernetes.io/auth-signin: https://$host/oauth2/start?rd=$escaped_request_uri
    nginx.ingress.kubernetes.io/auth-response-headers: X-Auth-Request-Id-Token
```

If your IdP issues JWT access tokens, `--set-xauthrequest --pass-access-token`
returns one as `X-Auth-Request-Access-Token` without the alpha config: set
`auth.human.header` to that and `auth.human.audience` to the access token's
audience.

### Gateway API

With `httpRoute.enabled`, auth is the gateway's policy, attached to the
pages route `<release>-marginalia-pages`. With Envoy Gateway and the same
oauth2-proxy, for example:

```yaml
httpRoute:
  enabled: true
  parentRefs:
    - name: public
      namespace: gateway-system
extraObjects:
  - apiVersion: gateway.envoyproxy.io/v1alpha1
    kind: SecurityPolicy
    metadata:
      name: '{{ include "marginalia.fullname" . }}-pages'
    spec:
      targetRefs:
        - group: gateway.networking.k8s.io
          kind: HTTPRoute
          name: '{{ include "marginalia.fullname" . }}-pages'
      extAuth:
        http:
          backendRefs:
            - name: oauth2-proxy
              namespace: auth
              port: 80
          path: /oauth2/auth
          headersToBackend: [X-Auth-Request-Id-Token]
```

`extraObjects` is templated, so it can name the release's own objects. It is
also the escape hatch for any routing the chart does not render: leave both
`ingress` and `httpRoute` off and bring your own.

## Agents: the public paths

Three paths must reach the host without the proxy's login in the way:

- `/mcp` — agents authenticate there themselves, with the bearer token from
  the Secret or an OAuth token (`auth.mcp.oauth.*`). A proxy that answers
  with a login redirect breaks every agent, and hides the 401 an OAuth client
  needs to find its authorization server.
- `/.well-known/oauth-protected-resource` — the metadata that 401 points to.
- `/healthz` — the probes, and anything watching from outside.

So the chart renders two objects per route kind: `<release>-marginalia-pages`
for `/`, which gets `ingress.pagesAnnotations` (the auth), and
`<release>-marginalia-public` for those three paths, which does not. The
more specific path wins on any controller that implements the spec.

## Values

`values.yaml` is the reference, with every value commented. The caps and
retentions under `limits` take the binary's own defaults when left empty;
the main [README](../../../README.md) lists them.

## Checks

- `scripts/helm-check.sh` lints the chart, renders each shape and checks
  each refusal, with nothing but helm.
- `scripts/helm-smoke.sh` builds the image from the checkout, installs the
  chart into a kind cluster, publishes a review over `/mcp`, checks that a
  page without a valid reviewer token is refused, and checks that the review
  survives the pod being replaced.

CI runs both.
