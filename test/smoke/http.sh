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
  # Wait for the server to exit: a shutdown still writing into $DATA made
  # rm -rf fail with "Directory not empty" on a passing run.
  [ -n "$SERVER_PID" ] && wait "$SERVER_PID" 2>/dev/null || true
  rm -rf "$DATA"
}
trap cleanup EXIT

echo "→ creating KBs in $DATA"
# serve never creates a KB (D906): kb create does.
for kb in "$KB1" "$KB2"; do
  "$BIN" kb create "$kb" --no-remote --local --data "$DATA" >/dev/null
done

echo "→ an off-loopback listen address with auth off must be refused (D906)"
if CARTOGRAPHER_AUTH=false "$BIN" serve --data="$DATA" --http="0.0.0.0:$PORT" >"$DATA/refused.log" 2>&1; then
  echo "serve started on 0.0.0.0 with auth off" >&2
  exit 1
fi
grep -q "authentication off" "$DATA/refused.log" || { echo "refusal does not say why:" >&2; cat "$DATA/refused.log" >&2; exit 1; }

echo "→ starting server on :$PORT"
# CARTOGRAPHER_AUTH=false: the machine's environment may have CARTOGRAPHER_TOKENS
# (client-side token), which in "auto" auth mode would turn on server auth.
CARTOGRAPHER_AUTH=false "$BIN" serve --data="$DATA" --http="127.0.0.1:$PORT" >/dev/null 2>&1 &
SERVER_PID=$!
# /ready, not /health: since D348 /health answers while the KBs are still
# being mounted, and /mcp is 503 until /ready turns 200.
for _ in $(seq 1 20); do
  curl -sf "http://127.0.0.1:$PORT/ready" >/dev/null 2>&1 && break
  sleep 0.5
done

# Check mounted KBs
kbs=$(curl -sf "http://127.0.0.1:$PORT/health" | python3 -c "import sys,json; d=json.load(sys.stdin); print([k['name'] for k in d['kbs']])")
echo "→ mounted KBs: $kbs"

# mcp_call fails the script on a tool error. Without this an unresolvable tool
# name is just a JSON-RPC *result* whose text says so, which curl -sf and the
# pipeline below both treat as success.
mcp_call() {
  local kb=$1 method=$2 args=$3 id=$4
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

# seed_index KB ID TITLE N creates an Index page and an overview page that links
# it, in one concept_batch with their curated index entries: the write gate (D907)
# refuses a page that is orphan or missing from its map's index.
seed_index() {
  local kb=$1 id=$2 title=$3 n=$4 map=${2%%/*}
  mcp_call "$kb" concept_batch "{\"operations\":[{\"op\":\"write\",\"id\":\"$id\",\"index_entry\":true,\"frontmatter\":{\"type\":\"Index\",\"title\":\"$title\"},\"body\":\"# $title\\n\\nOverview: [[$map/overview]]\\n\"},{\"op\":\"write\",\"id\":\"$map/overview\",\"index_entry\":true,\"frontmatter\":{\"type\":\"Index\",\"title\":\"$title overview\"},\"body\":\"# $title overview\\n\\nSee [[$id]].\\n\"}]}" "$n"
}

echo "→ maps $KB1"
mcp_call "$KB1" map_create '{"name":"notes","title":"Notes"}' 1
mcp_call "$KB1" map_create '{"name":"decisions","title":"Decisions"}' 2

echo "→ maps $KB2"
mcp_call "$KB2" map_create '{"name":"projects","title":"Projects"}' 3
mcp_call "$KB2" map_create '{"name":"ops","title":"Operations"}' 4

echo "→ expanded concept $KB1"
seed_index "$KB1" notes/quick-captures "Quick Captures" 5
mcp_call "$KB1" concept_expand '{"id":"notes/quick-captures"}' 6
seed_index "$KB1" decisions/architecture "Architecture Decisions" 7
mcp_call "$KB1" concept_expand '{"id":"decisions/architecture"}' 8

echo "→ expanded concept $KB2"
seed_index "$KB2" projects/cartographer "Cartographer" 9
mcp_call "$KB2" concept_expand '{"id":"projects/cartographer"}' 10
seed_index "$KB2" ops/runbooks "Runbooks" 11
mcp_call "$KB2" concept_expand '{"id":"ops/runbooks"}' 12

echo "→ atlas_overview $KB1"
mcp_call "$KB1" atlas_overview '{}' 13

echo "→ atlas_overview $KB2"
mcp_call "$KB2" atlas_overview '{}' 14

echo "✓ smoke test complete"
