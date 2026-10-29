#!/usr/bin/env bash
# Calls one tool on a hosted Marginalia over its remote MCP endpoint and prints
# the tool's structured result.
#
# Usage:
#   jq -n '{review_id: "…"}' | MARGINALIA_URL=https://… MARGINALIA_MCP_TOKEN=… \
#     scripts/marginalia-call.sh feedback_since
#
# The arguments are the JSON object on stdin. The transport is stateless
# (internal/mcpserver/http.go), so one POST of tools/call is the whole
# exchange — no initialize, no session id. The token travels in a header and
# is never printed, echoed or passed as an argument: process lists and logs
# show neither. Exit 1 when the call fails, naming the tool and the server's
# error message, never the request.
set -euo pipefail

tool="${1:-}"
[[ "$tool" =~ ^[a-z_]+$ ]] || {
  echo "marginalia-call: usage: marginalia-call.sh <tool> < arguments.json" >&2
  exit 2
}
: "${MARGINALIA_URL:?marginalia-call: MARGINALIA_URL is not set}"
: "${MARGINALIA_MCP_TOKEN:?marginalia-call: MARGINALIA_MCP_TOKEN is not set}"

args="$(cat)"
jq -e 'type == "object"' >/dev/null 2>&1 <<<"$args" || {
  echo "marginalia-call: arguments must be a JSON object" >&2
  exit 2
}

body="$(jq -c --arg tool "$tool" --argjson args "$args" \
  '{jsonrpc: "2.0", id: 1, method: "tools/call", params: {name: $tool, arguments: $args}}' <<<'{}')"

# The header comes from a file descriptor, not argv, so the token is not in
# curl's command line.
response="$(curl -sS --fail-with-body --max-time 120 -X POST "${MARGINALIA_URL%/}/mcp" \
  -H @<(printf 'Authorization: Bearer %s\n' "$MARGINALIA_MCP_TOKEN") \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  --data-binary @- <<<"$body")" || {
  echo "marginalia-call: $tool: the request failed" >&2
  exit 1
}

# Streamable HTTP answers either as JSON or as one SSE event.
msg="$(sed -n 's/^data: //p' <<<"$response")"
[ -n "$msg" ] || msg="$response"
if ! jq -e '.result' >/dev/null 2>&1 <<<"$msg"; then
  echo "marginalia-call: $tool: $(jq -r '.error.message // "no result"' <<<"$msg" 2>/dev/null || echo "unreadable response")" >&2
  exit 1
fi
if jq -e '.result.isError == true' >/dev/null <<<"$msg"; then
  echo "marginalia-call: $tool: $(jq -r '[.result.content[]?.text] | join(" ") | .[0:300]' <<<"$msg")" >&2
  exit 1
fi
jq '.result.structuredContent' <<<"$msg"
