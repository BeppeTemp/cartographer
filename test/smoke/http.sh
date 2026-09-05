#!/usr/bin/env bash
# test-kb-flow.sh — smoke test of the full KB flow: create, map, expanded concept, overview.
# Starts the native binary on a temp data dir, populates two KBs via MCP and checks the response.
# Usage: ./test/smoke/http.sh (or `make smoke-http`, which builds first)
set -euo pipefail

BASE="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$BASE/bin/cartographer"
[ -x "$BIN" ] || { echo "missing $BIN: run 'make build' first" >&2; exit 1; }

DATA="$(mktemp -d)"
PORT=18081
MCP="http://127.0.0.1:$PORT/mcp"

KB1="smoke-kb-1"
KB2="smoke-kb-2"
SERVER_PID=""

cleanup() {
  echo "→ cleanup: stopping server and removing $DATA"
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  rm -rf "$DATA"
}
trap cleanup EXIT

echo "→ creating KB folders in $DATA"
mkdir -p "$DATA/$KB1" "$DATA/$KB2"

echo "→ starting server on :$PORT"
# CARTOGRAPHER_AUTH=false: the machine's environment may have CARTOGRAPHER_TOKENS
# (client-side token), which in "auto" auth mode would turn on server auth.
CARTOGRAPHER_AUTH=false "$BIN" serve --data="$DATA" --init --http="127.0.0.1:$PORT" >/dev/null 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 20); do
  curl -sf "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && break
  sleep 0.5
done

# Check mounted KBs
kbs=$(curl -sf "http://127.0.0.1:$PORT/health" | python3 -c "import sys,json; d=json.load(sys.stdin); print([k['name'] for k in d['kbs']])")
echo "→ mounted KBs: $kbs"

# tool_prefix() reports the effective tool-name prefix the server derived for a
# KB. Since D153 that defaults to the KB name, so the bare tool name does not
# resolve and every call in this script silently came back "tool not found"
# while the script still reported success. /health advertises the prefix each
# KB was actually mounted with, which is more honest than re-deriving the
# sanitisation rules here.
tool_prefix() {
  curl -sf "http://127.0.0.1:$PORT/health" \
    | python3 -c "import sys,json; kb=sys.argv[1]; d=json.load(sys.stdin); print(next((k.get('tool_prefix','') for k in d['kbs'] if k['name']==kb), ''))" "$1"
}

# mcp_call fails the script on a tool error. Without this an unresolvable tool
# name is just a JSON-RPC *result* whose text says so, which curl -sf and the
# pipeline below both treat as success.
mcp_call() {
  local kb=$1 method=$2 args=$3 id=$4 prefix
  prefix=$(tool_prefix "$kb")
  [ -n "$prefix" ] && method="${prefix}__${method}"
  curl -sf -X POST "$MCP?kb=$kb" \
    -H "Content-Type: application/json" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$id,\"method\":\"tools/call\",\"params\":{\"name\":\"$method\",\"arguments\":$args}}" \
    | python3 -c "
import sys, json
d = json.load(sys.stdin)
if 'error' in d:
    sys.exit('MCP error calling $method: %s' % d['error'])
r = d['result']
text = r['content'][0]['text']
print(text)
if r.get('isError'):
    sys.exit('tool $method returned an error for KB $kb: %s' % text)
"
}

echo "→ maps $KB1"
mcp_call "$KB1" map_create '{"name":"notes","title":"Notes"}' 1
mcp_call "$KB1" map_create '{"name":"decisions","title":"Decisions"}' 2

echo "→ maps $KB2"
mcp_call "$KB2" map_create '{"name":"projects","title":"Projects"}' 3
mcp_call "$KB2" map_create '{"name":"ops","title":"Operations"}' 4

echo "→ expanded concept $KB1"
mcp_call "$KB1" concept_write '{"id":"notes/quick-captures","frontmatter":{"type":"Index","title":"Quick Captures"},"body":"# Quick Captures\n"}' 5
mcp_call "$KB1" concept_expand '{"id":"notes/quick-captures"}' 6
mcp_call "$KB1" concept_write '{"id":"decisions/architecture","frontmatter":{"type":"Index","title":"Architecture Decisions"},"body":"# Architecture Decisions\n"}' 7
mcp_call "$KB1" concept_expand '{"id":"decisions/architecture"}' 8

echo "→ expanded concept $KB2"
mcp_call "$KB2" concept_write '{"id":"projects/cartographer","frontmatter":{"type":"Index","title":"Cartographer"},"body":"# Cartographer\n"}' 9
mcp_call "$KB2" concept_expand '{"id":"projects/cartographer"}' 10
mcp_call "$KB2" concept_write '{"id":"ops/runbooks","frontmatter":{"type":"Index","title":"Runbooks"},"body":"# Runbooks\n"}' 11
mcp_call "$KB2" concept_expand '{"id":"ops/runbooks"}' 12

echo "→ atlas_overview $KB1"
mcp_call "$KB1" atlas_overview '{}' 13

echo "→ atlas_overview $KB2"
mcp_call "$KB2" atlas_overview '{}' 14

echo "✓ smoke test complete"
