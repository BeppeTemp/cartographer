#!/usr/bin/env bash
# Smoke test of the demo image (D751, D2053): readiness, a signed audit
# log, a request without the token, a read tool on both KBs (astronomy and the
# documentation), refused writes on both, the routed tool schema, and the docs
# KB carrying the version of the image. Exits non-zero on any deviation.
#
# Usage: deploy/demo/smoke.sh <image>
set -euo pipefail

image=${1:?usage: deploy/demo/smoke.sh <image>}
read_token=smoke-read-token
name=cartographer-demo-smoke-$$
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

fail() { echo "smoke: $1" >&2; docker logs "$name" >&2 2>&1 || true; exit 1; }

docker run -d --name "$name" -p 127.0.0.1:0:8080 \
  -e "CARTOGRAPHER_TOKENS=$read_token|kb:astronomy:r;kb:cartographer-docs:r" \
  -e K_SERVICE=cartographer-demo "$image" >/dev/null
port=$(docker port "$name" 8080/tcp | head -1 | sed 's/.*://')
url=http://127.0.0.1:$port
# /ready needs the token once auth is on (deployment.md §Observability), so it
# is no probe target; /health is public and carries ready.
ready=
for _ in $(seq 100); do
  curl -sf "$url/health" 2>/dev/null | grep -q '"ready":true' && { ready=1; break; }
  sleep 0.3
done
[ -n "$ready" ] || fail "/health never reported ready:true"

# rpc <token-or-empty> <json> → body on stdout, "\n<status>" last. The routed
# mount is what the Worker maps /mcp to; a bare /mcp on a multi-KB server asks
# for a kb parameter.
# (the ${auth[@]+...} form keeps an empty array legal under set -u on bash 3.2)
rpc() {
  local auth=()
  [ -n "$1" ] && auth=(-H "Authorization: Bearer $1")
  curl -s -w '\n%{http_code}' -X POST "$url/mcp/routed" ${auth[@]+"${auth[@]}"} \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$2"
}
status() { printf '%s\n' "$1" | tail -1; }
body() { printf '%s\n' "$1" | sed '$d'; }
# The tests below read a response with here-strings, not pipes: under pipefail a
# grep -q that exits early would SIGPIPE the writer and fail a passing check.
is_error() { grep -q '"error"\|"isError":true' <<<"$1"; }
has() { grep -q -- "$2" <<<"$1"; }
call() { # call <id> <tool> <arguments-json>
  rpc "$read_token" '{"jsonrpc":"2.0","id":'"$1"',"method":"tools/call","params":{"name":"'"$2"'","arguments":'"$3"'}}'
}
init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'

# The image's data directory is writable, so the server generated its audit key
# there and signs the log.
case $(docker logs "$name" 2>&1) in
*"audit log: "*", signed;"*) ;;
*) fail "the audit log is not signed" ;;
esac

r=$(rpc "" "$init")
[ "$(status "$r")" = 401 ] || fail "request without token: want 401, got $(status "$r")"

r=$(rpc "$read_token" "$init")
[ "$(status "$r")" = 200 ] || fail "initialize: status $(status "$r")"
has "$(body "$r")" '"serverInfo"' || fail "initialize: no serverInfo"
has "$(body "$r")" 'astronomy' && has "$(body "$r")" 'cartographer-docs' || fail "initialize: the instructions do not name both KBs: $(body "$r" | head -c 600)"

r=$(rpc "$read_token" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
[ "$(status "$r")" = 200 ] && has "$(body "$r")" '"search"' || fail "tools/list: no search tool"
# Two KBs: every tool takes kb, required, a plain string with no enum (D1183):
# the valid names are in the server instructions of initialize, so the tool list
# does not grow with the KBs.
has "$(body "$r")" '"kb": *{ *"description": *"[^"]*", *"type": *"string" *}' || fail "tools/list: kb is not a plain string: $(body "$r" | head -c 600)"
has "$(body "$r")" '"enum": *\[ *"astronomy"' && fail "tools/list: kb is still an enum of the KBs: $(body "$r" | head -c 600)"
has "$(body "$r")" '"required":\[[^]]*"kb"' || fail "tools/list: kb is not required"

# The documentation KB answers the question the demo is for.
r=$(call 3 search '{"kb":"cartographer-docs","query":"workspace bind"}')
[ "$(status "$r")" = 200 ] || fail "search on cartographer-docs: status $(status "$r")"
is_error "$(body "$r")" && fail "search on cartographer-docs was refused: $(body "$r")"
has "$(body "$r")" 'workspace' || fail "search on cartographer-docs found nothing for workspace bind: $(body "$r" | head -c 600)"

r=$(call 4 search '{"kb":"astronomy","query":"star"}')
[ "$(status "$r")" = 200 ] || fail "search on astronomy: status $(status "$r")"
is_error "$(body "$r")" && fail "search on astronomy was refused: $(body "$r")"

for kb in astronomy cartographer-docs; do
  r=$(call 5 concept_write '{"kb":"'"$kb"'","path":"data/smoke.md","content":"x"}')
  is_error "$(body "$r")" || fail "concept_write with the read token was NOT refused on $kb: $(body "$r")"
done

# The documentation is the one of this image: the root page names the ref the
# pinned URLs point at, the version tag for a release and main for any other build.
version=$(docker run --rm "$image" version | awk 'NR == 1 { print $NF }')
case $version in
v[0-9]*.[0-9]*.[0-9]* | [0-9]*.[0-9]*.[0-9]*) want=v${version#v} ;;
*) want=main ;;
esac
r=$(call 6 atlas_overview '{"kb":"cartographer-docs"}')
[ "$(status "$r")" = 200 ] || fail "atlas_overview on cartographer-docs: status $(status "$r")"
has "$(body "$r")" "Cartographer $want " || has "$(body "$r")" "Cartographer $want\\." ||
  fail "the documentation KB does not carry the image's ref ($want): $(body "$r" | head -c 600)"

echo "smoke: ok ($image)"
