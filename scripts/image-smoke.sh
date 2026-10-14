#!/usr/bin/env bash
# Smoke-tests the hosted image as a platform would run it: the amd64 image a
# goreleaser snapshot just built, started with no volume, a file:///tmp store
# and secrets from the environment, as a non-root user. It must answer the
# health check and accept a publish, and the image must carry no secret.
#
# Usage: ./scripts/image-smoke.sh [image]   (default: the snapshot's amd64 tag)
set -euo pipefail

image="${1:-$(docker images 'ghcr.io/gruesomeparty/marginalia' --format '{{.Repository}}:{{.Tag}}' | grep -- '-amd64$' | head -1)}"
[ -n "$image" ] || { echo "image-smoke: no amd64 image found — run goreleaser release --snapshot first" >&2; exit 2; }
echo "image-smoke: $image"

token="smoke-$(date +%s)"
name="marginalia-smoke-$$"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# The host needs a human identity to start; off loopback that is the edge's
# JWT, and an issuer under cloudflareaccess.com needs no network to configure
# (its key-set URL is known by convention and fetched only when a page is
# requested). Nothing here opens a page.
docker run -d --name "$name" -p 18080:8080 \
  -e MARGINALIA_STORE=file:///tmp/marginalia-smoke \
  -e MARGINALIA_MCP_TOKEN="$token" \
  -e MARGINALIA_HUMAN_JWT_ISSUER=https://smoke.cloudflareaccess.com \
  -e MARGINALIA_HUMAN_JWT_AUDIENCE=smoke \
  "$image" >/dev/null

user="$(docker inspect --format '{{.Config.User}}' "$name")"
case "$user" in
  ""|0|root|0:0|root:root) echo "image-smoke: the image runs as root ($user)" >&2; exit 1 ;;
esac
echo "image-smoke: user $user"

for _ in $(seq 1 50); do
  if code="$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/healthz 2>/dev/null)" && [ "$code" = "200" ]; then
    break
  fi
  sleep 0.2
done
[ "${code:-}" = "200" ] || { echo "image-smoke: /healthz answered ${code:-nothing}" >&2; docker logs "$name" >&2; exit 1; }
echo "image-smoke: /healthz 200"

# publish_document over the MCP transport, with the token.
body='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"publish_document","arguments":{"title":"smoke","documents":[{"name":"smoke.md","content":"# Smoke\n\nIt runs.\n"}]}}}'
response="$(curl -s -X POST http://127.0.0.1:18080/mcp \
  -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' -d "$body")"
review="$(printf '%s\n' "$response" | sed -n 's/^data: //p' | jq -r '.result.structuredContent.review_id // empty')"
[ -n "$review" ] || { echo "image-smoke: publish_document did not return a review id:" >&2; printf '%s\n' "$response" >&2; docker logs "$name" >&2; exit 1; }
echo "image-smoke: published review $review"

# The token must not be in the image: it arrived in the environment.
if docker history --no-trunc "$image" | grep -q -- "$token"; then
  echo "image-smoke: the token appears in the image history" >&2; exit 1
fi
if docker history --no-trunc "$image" | grep -qiE 'MARGINALIA_(MCP_TOKEN|R2_SECRET)'; then
  echo "image-smoke: a secret variable is baked into a layer" >&2; exit 1
fi
echo "image-smoke: no secrets in the image"
docker stop "$name" >/dev/null
echo "image-smoke: ok"
