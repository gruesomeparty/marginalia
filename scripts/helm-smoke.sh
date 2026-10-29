#!/usr/bin/env bash
# Installs deploy/helm/marginalia into a throwaway kind cluster, from an
# image built from this checkout, and checks what the chart promises: the
# pod comes up non-root and healthy, agents reach /mcp with the token from
# the existing Secret, pages refuse a request with no reviewer token, and a
# review survives the pod being replaced — the store is on the volume.
#
# Usage: ./scripts/helm-smoke.sh   (needs docker, kind, kubectl, helm, jq)
#        KEEP=1 leaves the cluster up.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cluster="${CLUSTER:-marginalia-smoke}"
ns=marginalia
release=smoke
svc="$release-marginalia"
token="smoke-$(date +%s)"
work="$(mktemp -d)"
pf=""
cleanup() {
  [ -n "$pf" ] && kill "$pf" 2>/dev/null || true
  rm -rf "$work"
  if [ -z "${KEEP:-}" ]; then kind delete cluster --name "$cluster" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
say() { echo "helm-smoke: $*"; }
fail() {
  echo "helm-smoke: $*" >&2
  kubectl -n "$ns" get pods -o wide >&2 || true
  kubectl -n "$ns" logs -l app.kubernetes.io/instance="$release" --tail=50 >&2 || true
  exit 1
}

# The image a release would ship, built from this checkout: the released
# Dockerfile around a static linux binary.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -C "$root" -o "$work/marginalia" .
cp "$root/Dockerfile" "$work/"
docker build -q -t marginalia:smoke "$work" >/dev/null
say "built marginalia:smoke"

kind get clusters 2>/dev/null | grep -qx "$cluster" || kind create cluster --name "$cluster" --wait 120s
kind load docker-image marginalia:smoke --name "$cluster"
kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$ns" create secret generic marginalia --from-literal=MARGINALIA_MCP_TOKEN="$token" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

# The issuer is never reached: the key-set URL is given, so nothing is
# discovered at startup, and no request here carries a real token.
helm upgrade --install "$release" "$root/deploy/helm/marginalia" -n "$ns" --wait --timeout 4m \
  --set image.repository=marginalia --set image.tag=smoke --set image.pullPolicy=Never \
  --set publicURL=https://review.example.com \
  --set auth.human.issuer=https://idp.invalid --set auth.human.audience=smoke \
  --set auth.human.jwksURL=https://idp.invalid/keys \
  --set existingSecret=marginalia --set persistence.size=100Mi \
  --set networkPolicy.enabled=true >/dev/null || fail "install did not become ready"
say "installed"
helm test "$release" -n "$ns" >/dev/null || fail "helm test failed"
say "helm test passed"

user="$(kubectl -n "$ns" get pod -l app.kubernetes.io/instance="$release" -o jsonpath='{.items[0].spec.securityContext.runAsUser}')"
if [ -z "$user" ] || [ "$user" = 0 ]; then fail "the pod runs as root"; fi

forward() {
  [ -n "$pf" ] && kill "$pf" 2>/dev/null || true
  kubectl -n "$ns" port-forward "svc/$svc" 18080:80 >/dev/null 2>&1 &
  pf=$!
  for _ in $(seq 1 50); do
    curl -fs -o /dev/null http://127.0.0.1:18080/healthz && return 0
    sleep 0.2
  done
  fail "/healthz unreachable through the service"
}
mcp() { # mcp <tool> <arguments json>
  local body
  body="$(jq -nc --arg t "$1" --argjson a "$2" '{jsonrpc:"2.0",id:1,method:"tools/call",params:{name:$t,arguments:$a}}')"
  curl -s -X POST http://127.0.0.1:18080/mcp -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$body" |
    sed -n 's/^data: //p'
}

forward
review="$(mcp publish_document '{"title":"smoke","documents":[{"name":"smoke.md","content":"# Smoke\n\nIt runs.\n"}]}' |
  jq -r '.result.structuredContent.review_id // empty')"
[ -n "$review" ] || fail "publish_document returned no review id"
say "published review $review"

code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:18080/r/$review/")"
[ "$code" = 401 ] || fail "a page with no reviewer token answered $code, want 401"
code="$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Auth-Request-Id-Token: not.a.token' "http://127.0.0.1:18080/r/$review/")"
[ "$code" = 401 ] || fail "a page with a forged token answered $code, want 401"
say "pages refuse requests without a valid reviewer token"

kubectl -n "$ns" delete pod -l app.kubernetes.io/instance="$release" --wait=true >/dev/null
kubectl -n "$ns" rollout status "deploy/$svc" --timeout 3m >/dev/null || fail "the replacement pod did not come up"
forward
status="$(mcp review_status "$(jq -nc --arg id "$review" '{review_id:$id}')")"
[ "$(jq -r '.result.isError // false' <<<"$status")" = false ] && [ -n "$(jq -r '.result // empty' <<<"$status")" ] ||
  fail "review $review did not survive the pod being replaced: $status"
say "review survived a pod replacement"
say "ok"
