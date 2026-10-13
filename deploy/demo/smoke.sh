#!/usr/bin/env bash
# Smoke test of the demo image (D751): readiness, a read tool, a refused write
# tool, and a request without the token. Exits non-zero on any deviation.
#
# Usage: deploy/demo/smoke.sh <image>
set -euo pipefail

image=${1:?usage: deploy/demo/smoke.sh <image>}
token=smoke-token
name=cartographer-demo-smoke-$$
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --name "$name" -p 127.0.0.1:0:8080 \
  -e "CARTOGRAPHER_TOKENS=$token|kb:astronomy:r" "$image" >/dev/null
port=$(docker port "$name" 8080/tcp | head -1 | sed 's/.*://')
url=http://127.0.0.1:$port

# /ready needs the token once auth is on (deployment.md §Observability), so it
# is no probe target; /health is public and carries ready.
ready=
for _ in $(seq 100); do
  curl -sf "$url/health" 2>/dev/null | grep -q '"ready":true' && { ready=1; break; }
  sleep 0.3
done
[ -n "$ready" ] || { echo "smoke: /health never reported ready:true" >&2; docker logs "$name" >&2; exit 1; }

fail() { echo "smoke: $1" >&2; docker logs "$name" >&2; exit 1; }
# rpc <token-or-empty> <json> → body on stdout, "\n<status>" last
# (the ${auth[@]+...} form keeps an empty array legal under set -u on bash 3.2)
rpc() {
  local auth=()
  [ -n "$1" ] && auth=(-H "Authorization: Bearer $1")
  curl -s -w '\n%{http_code}' -X POST "$url/mcp" ${auth[@]+"${auth[@]}"} \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$2"
}
status() { printf '%s\n' "$1" | tail -1; }
body() { printf '%s\n' "$1" | sed '$d'; }

r=$(rpc "" '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}')
[ "$(status "$r")" = 401 ] || fail "request without token: want 401, got $(status "$r")"

r=$(rpc "$token" '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}')
[ "$(status "$r")" = 200 ] || fail "initialize: status $(status "$r")"
body "$r" | grep -q '"serverInfo"' || fail "initialize: no serverInfo"

r=$(rpc "$token" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
[ "$(status "$r")" = 200 ] && body "$r" | grep -q '"search"' || fail "tools/list: no search tool"

r=$(rpc "$token" '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"kb":"astronomy","query":"star"}}}')
[ "$(status "$r")" = 200 ] || fail "search: status $(status "$r")"
body "$r" | grep -q '"error"\|"isError":true' && fail "search was refused: $(body "$r")"

r=$(rpc "$token" '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_write","arguments":{"kb":"astronomy","path":"data/smoke.md","content":"x"}}}')
body "$r" | grep -q '"error"\|"isError":true' || fail "concept_write was NOT refused: $(body "$r")"
echo "smoke: ok ($image)"
