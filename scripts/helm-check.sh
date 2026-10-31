#!/usr/bin/env bash
# Checks deploy/helm/marginalia without a cluster: the chart lints, renders
# in each of its shapes, and refuses at template time each setting the host
# would otherwise refuse in a crash loop.
#
# Usage: ./scripts/helm-check.sh   (needs helm on PATH)
set -euo pipefail

chart="$(dirname "$0")/../deploy/helm/marginalia"
base=(--set publicURL=https://review.example.com
      --set auth.human.issuer=https://idp.example.com
      --set auth.human.audience=marginalia
      --set existingSecret=marginalia)
render() { helm template check "$chart" "${base[@]}" "$@"; }

helm lint --strict "$chart" "${base[@]}"

# Each shape renders, and says what it should.
expect() { # expect <description> <pattern> -- <helm args…>
  local what="$1" pattern="$2"; shift 3
  if ! render "$@" | grep -qE -- "$pattern"; then
    echo "helm-check: $what: no match for /$pattern/" >&2; exit 1
  fi
  echo "helm-check: ok: $what"
}
expect "one replica, replaced not rolled" 'type: Recreate' --
expect "a file store on the volume" 'value: "file:///data"' --
expect "the chart's own claim" 'kind: PersistentVolumeClaim' --
expect "the proxy's header reaches the host" 'MARGINALIA_HUMAN_JWT_HEADER' --
expect "an ingress for pages" 'name: check-marginalia-pages' -- --set ingress.enabled=true
expect "agents bypass the proxy" 'path: /mcp' -- --set ingress.enabled=true
expect "the agent long-poll bypasses it too" 'path: /agent/' -- --set ingress.enabled=true
expect "the gateway's public route has it" 'value: /agent/' -- --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gw'
expect "a reviewer allow-list" 'value: "a@example.com,b@example.com"' -- --set 'auth.human.reviewers={a@example.com,b@example.com}'
expect "auth annotations on pages" 'auth-url: x' -- --set ingress.enabled=true --set ingress.pagesAnnotations.nginx\\.ingress\\.kubernetes\\.io/auth-url=x
expect "gateway API routes" 'kind: HTTPRoute' -- --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gw'
expect "an existing claim" 'claimName: mine' -- --set persistence.existingClaim=mine
expect "a digest pin" 'marginalia@sha256:abc' -- --set image.digest=sha256:abc
expect "extra objects, templated" "name: '?check-extra" -- --set-json 'extraObjects=[{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"{{ .Release.Name }}-extra"}}]'

# A number written in a values file is a float to helm; it must still reach
# the host as an integer, not 1e+07.
numbers="$(mktemp)"
trap 'rm -f "$numbers"' EXIT
printf 'limits:\n  documentBytes: 10000000\n  openReviews: 50\nextraEnv:\n  SOME_NUMBER: 25000000\n  A_FRACTION: 0.5\n' >"$numbers"
expect "numbers from a values file stay integers" 'value: "10000000"' -- -f "$numbers"
expect "extraEnv numbers too" 'value: "25000000"' -- -f "$numbers"
expect "a fraction is not truncated" 'value: "0.5"' -- -f "$numbers"
expect "helm test is let through a narrowed policy" 'component: helm-test' -- --set networkPolicy.enabled=true --set-json 'networkPolicy.from=[{"namespaceSelector":{"matchLabels":{"name":"ingress"}}}]' --show-only templates/networkpolicy.yaml
if render --set networkPolicy.enabled=true --set networkPolicy.allowHelmTest=false --set-json 'networkPolicy.from=[{"namespaceSelector":{}}]' --show-only templates/networkpolicy.yaml | grep -q helm-test; then
  echo "helm-check: allowHelmTest=false still admits the test pod" >&2; exit 1
fi
echo "helm-check: ok: the helm test exception can be turned off"

# Auth annotations stay off the public paths.
if render --set ingress.enabled=true --set ingress.pagesAnnotations.auth=x --show-only templates/ingress.yaml \
   | awk '/name: check-marginalia-public/{p=1} p' | grep -q 'auth: x'; then
  echo "helm-check: the pages' auth annotation leaked onto the public ingress" >&2; exit 1
fi
echo "helm-check: ok: public ingress carries no auth"
if render --set persistence.existingClaim=mine | grep -q 'kind: PersistentVolumeClaim'; then
  echo "helm-check: an existing claim still rendered one of its own" >&2; exit 1
fi

# Settings the host refuses are refused here first.
refuse() { # refuse <message fragment> <helm args…>
  local want="$1"; shift
  local out
  if out="$(render "$@" 2>&1)"; then
    echo "helm-check: rendered, but should have refused: $*" >&2; exit 1
  fi
  grep -q -- "$want" <<<"$out" || { echo "helm-check: refused $* for the wrong reason: $out" >&2; exit 1; }
  echo "helm-check: ok: refuses $*"
}
refuse "publicURL is required" --set publicURL=
refuse "scheme and host only" --set publicURL=https://review.example.com/sub
refuse "both required" --set auth.human.audience=
refuse "cannot be Authorization" --set auth.human.header=Authorization
refuse "need a credential" --set existingSecret=
refuse "not both" --set ingress.enabled=true --set httpRoute.enabled=true
refuse "parentRefs is required" --set httpRoute.enabled=true
echo "helm-check: ok"
