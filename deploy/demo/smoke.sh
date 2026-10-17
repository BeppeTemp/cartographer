#!/usr/bin/env bash
# Smoke test of the demo image (D751, D800): readiness, a read tool, refused
# writes on both KBs, one accepted guestbook write that reaches its git remote,
# the routed tool schema, a request without the token, and a start with an
# unreachable guestbook remote. Exits non-zero on any deviation.
#
# Usage: deploy/demo/smoke.sh <image>
#
# The guestbook remote is a bare repository on a Docker volume, seeded from
# demo/guestbook/ and reached through file://, with a dummy token file: the
# image's entrypoint and the server's pull/push run as in production, minus the
# network.
set -euo pipefail

image=${1:?usage: deploy/demo/smoke.sh <image>}
repo=$(cd "$(dirname "$0")/../.." && pwd)
read_token=smoke-read-token
write_token=smoke-write-token
name=cartographer-demo-smoke-$$
volume=$name-remote
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm -f "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() { echo "smoke: $1" >&2; docker logs "$name" >&2 2>&1 || true; exit 1; }

# --- the remote: a bare repo seeded from demo/guestbook, owned by the server's user
docker volume create "$volume" >/dev/null
tar -C "$repo/demo/guestbook" -c . | docker run -i --rm -u root -v "$volume:/remote" --entrypoint sh "$image" -ec '
  git config --global --add safe.directory "*"
  mkdir /seed /remote/tokens
  tar -x -C /seed
  cd /seed
  git init -q -b main
  git add -A
  git -c user.name=seed -c user.email=seed@example.com commit -qm "Seed the guestbook"
  git init -q --bare -b main /remote/guestbook.git
  git push -q /remote/guestbook.git main
  echo dummy-token >/remote/tokens/guestbook.token
  chown -R cartographer /remote
'
remote_git() { docker run --rm -v "$volume:/remote" --entrypoint git "$image" -C /remote/guestbook.git "$@"; }

# start <remote-url>: runs the image with both tokens and waits for /health ready.
start() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker run -d --name "$name" -p 127.0.0.1:0:8080 -v "$volume:/remote" \
    -e "CARTOGRAPHER_TOKENS=$read_token|kb:astronomy:r;kb:guestbook:r,$write_token|kb:guestbook:rw" \
    -e "GUESTBOOK_REMOTE=$1" -e CARTOGRAPHER_GIT_TOKEN_DIR=/remote/tokens \
    -e K_SERVICE=cartographer-demo "$image" >/dev/null
  local port
  port=$(docker port "$name" 8080/tcp | head -1 | sed 's/.*://')
  url=http://127.0.0.1:$port
  # /ready needs the token once auth is on (deployment.md §Observability), so it
  # is no probe target; /health is public and carries ready.
  local ready=
  for _ in $(seq 100); do
    curl -sf "$url/health" 2>/dev/null | grep -q '"ready":true' && { ready=1; break; }
    sleep 0.3
  done
  [ -n "$ready" ] || fail "/health never reported ready:true"
}

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
init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'

# --- two KBs: the clone worked, both are mounted ---------------------------------
start file:///remote/guestbook.git

r=$(rpc "" "$init")
[ "$(status "$r")" = 401 ] || fail "request without token: want 401, got $(status "$r")"

r=$(rpc "$read_token" "$init")
[ "$(status "$r")" = 200 ] || fail "initialize: status $(status "$r")"
has "$(body "$r")" '"serverInfo"' || fail "initialize: no serverInfo"

r=$(rpc "$read_token" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
[ "$(status "$r")" = 200 ] && has "$(body "$r")" '"search"' || fail "tools/list: no search tool"
# Two KBs: every tool takes kb, required, an enum of both.
has "$(body "$r")" '"enum":\["astronomy","guestbook"\]' || fail "tools/list: kb is not an enum of astronomy and guestbook: $(body "$r" | head -c 600)"
has "$(body "$r")" '"required":\[[^]]*"kb"' || fail "tools/list: kb is not required"

r=$(rpc "$read_token" '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"kb":"astronomy","query":"star"}}}')
[ "$(status "$r")" = 200 ] || fail "search: status $(status "$r")"
is_error "$(body "$r")" && fail "search was refused: $(body "$r")"

for kb in astronomy guestbook; do
  r=$(rpc "$read_token" '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_write","arguments":{"kb":"'"$kb"'","path":"data/smoke.md","content":"x"}}}')
  is_error "$(body "$r")" || fail "concept_write with the read token was NOT refused on $kb: $(body "$r")"
done

msg='"template":"message","id":"messages/2026-01-01-smoke","vars":{"client":"Smoke test","message":"hello from the smoke test"}'
r=$(rpc "$read_token" '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"concept_new","arguments":{"kb":"guestbook",'"$msg"'}}}')
is_error "$(body "$r")" || fail "concept_new with the read token was NOT refused: $(body "$r")"

r=$(rpc "$write_token" '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"concept_new","arguments":{"kb":"astronomy",'"$msg"'}}}')
is_error "$(body "$r")" || fail "concept_new on astronomy with the write token was NOT refused: $(body "$r")"

r=$(rpc "$write_token" '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"concept_new","arguments":{"kb":"guestbook",'"$msg"'}}}')
[ "$(status "$r")" = 200 ] || fail "concept_new with the write token: status $(status "$r")"
is_error "$(body "$r")" && fail "concept_new with the write token was refused: $(body "$r")"
# The push is synchronous on Cloud Run (K_SERVICE, set by `start` below): the
# commit is in the remote by the time the call returned.
pushed=$(remote_git log --format=%s main)
case $pushed in
*"concept_new: messages/2026-01-01-smoke"*) ;;
*) fail "the message commit never reached the guestbook remote: $pushed" ;;
esac

r=$(rpc "$write_token" '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"concept_new","arguments":{"kb":"guestbook",'"$msg"'}}}')
is_error "$(body "$r")" || fail "a second identical concept_new was NOT refused: $(body "$r")"

# --- the guestbook is down: astronomy still serves, alone -------------------------
start file:///nonexistent/guestbook.git
logs=$(docker logs "$name" 2>&1)
case $logs in
*"guestbook not cloned"*) ;;
*) fail "the failed clone was not logged" ;;
esac

r=$(rpc "$read_token" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
[ "$(status "$r")" = 200 ] && has "$(body "$r")" '"search"' || fail "tools/list without the guestbook: no search tool"
# One KB: kb is not a property of any tool.
has "$(body "$r")" '"kb"' && fail "tools/list with one KB still advertises kb: $(body "$r" | head -c 600)"
r=$(rpc "$read_token" '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"star"}}}')
[ "$(status "$r")" = 200 ] || fail "search without the guestbook: status $(status "$r")"
is_error "$(body "$r")" && fail "search without the guestbook was refused: $(body "$r")"

echo "smoke: ok ($image)"
