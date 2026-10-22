#!/usr/bin/env bash
# Shared by test/smoke/http.sh and test/smoke/image.sh (D995): one MCP tool call
# over HTTP. Source it; it sets nothing by itself.
#
# Reads $MCP (the /mcp URL) and, when set, $MCP_TOKEN (sent as a Bearer token).

# mcp_call KB TOOL ARGS-JSON ID fails the script on a tool error. Without this an
# unresolvable tool name is just a JSON-RPC *result* whose text says so, which
# curl -sf and the pipeline below both treat as success.
mcp_call() {
  local kb=$1 method=$2 args=$3 id=$4
  local auth=()
  [ -n "${MCP_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $MCP_TOKEN")
  # (the ${auth[@]+...} form keeps an empty array legal under set -u on bash 3.2)
  curl -sf -X POST "$MCP?kb=$kb" ${auth[@]+"${auth[@]}"} \
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
