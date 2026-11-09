#!/usr/bin/env bash
# Smoke test of the server image (D995): the tools the runtime stage promises, a
# non-root user, a KB created and served over HTTP with a token, and one write
# that lands as a git commit. What an operator does with the image, on the
# runner's own architecture. Exits non-zero on any deviation.
#
# Usage: test/smoke/image.sh <image> [expected-version]   (or `make docker-smoke`)
set -euo pipefail

image=${1:?usage: test/smoke/image.sh <image> [expected-version]}
want_version=${2:-}
repo=$(cd "$(dirname "$0")/../.." && pwd)
token=smoke-admin
name=cartographer-image-smoke-$$
volume=$name-data
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm -f "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() { echo "smoke: $1" >&2; docker logs "$name" >&2 2>&1 || true; exit 1; }

echo "→ tools in the image"
docker run --rm --entrypoint sh "$image" -c 'git --version && sops --version && age --version && ssh -V' \
  >/dev/null 2>&1 || fail "git, sops, age or ssh is missing from the image"
version=$(docker run --rm "$image" version) || fail "'version' failed"
echo "  $version"
if [ -n "$want_version" ]; then
  case $version in *"$want_version"*) ;; *) fail "version '$version' does not carry the VERSION build-arg '$want_version'" ;; esac
fi
# The worker is the same image: the subcommand is there and needs no volume
# (no --config prints usage and exits 2).
rc=0
docker run --rm "$image" worker >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "'worker' without --config exited $rc, want 2"
uid=$(docker run --rm --entrypoint id "$image" -u)
[ "$uid" != 0 ] || fail "the image runs as root"

echo "→ a KB on a data volume"
# A fresh named volume is root-owned; the server runs as the image's user.
docker volume create "$volume" >/dev/null
docker run --rm -u root -v "$volume:/data" --entrypoint chown "$image" cartographer /data
docker run --rm -v "$volume:/data" "$image" kb create smoke --no-remote --local --data /data >/dev/null \
  || fail "kb create failed"

echo "→ serving over HTTP"
# Off loopback with auth off is refused (test/smoke/http.sh), so the token is the
# way in, as for an operator.
docker run -d --name "$name" -p 127.0.0.1:0:8080 -v "$volume:/data" \
  -e "CARTOGRAPHER_TOKENS=$token" \
  "$image" serve --data /data --http 0.0.0.0:8080 >/dev/null
port=$(docker port "$name" 8080/tcp | head -1 | sed 's/.*://')
url=http://127.0.0.1:$port
ready=
for _ in $(seq 100); do
  curl -sf "$url/health" 2>/dev/null | grep -q '"ready":true' && { ready=1; break; }
  sleep 0.3
done
[ -n "$ready" ] || fail "/health never reported ready:true"

# shellcheck source=../lib/mcp.sh
. "$repo/test/lib/mcp.sh"
MCP=$url/mcp
MCP_TOKEN=$token
rpc() { # rpc <token-or-empty> <json> → body, "\n<status>" last
  local auth=()
  [ -n "$1" ] && auth=(-H "Authorization: Bearer $1")
  curl -s -w '\n%{http_code}' -X POST "$MCP?kb=smoke" ${auth[@]+"${auth[@]}"} \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$2"
}
init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'

echo "→ MCP"
r=$(rpc "" "$init")
[ "$(printf '%s\n' "$r" | tail -1)" = 401 ] || fail "request without token: want 401, got $(printf '%s\n' "$r" | tail -1)"
r=$(rpc "$token" "$init")
[ "$(printf '%s\n' "$r" | tail -1)" = 200 ] || fail "initialize: status $(printf '%s\n' "$r" | tail -1)"
# Here-strings, not pipes: under pipefail a grep -q that exits early would
# SIGPIPE the writer and fail a passing check.
grep -q '"serverInfo"' <<<"$r" || fail "initialize: no serverInfo"
r=$(rpc "$token" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
for tool in concept_write search; do
  grep -q "\"$tool\"" <<<"$r" || fail "tools/list: no $tool tool"
done

before=$(docker exec "$name" git -C /data/smoke rev-list --count HEAD)
mcp_call smoke map_create '{"name":"notes","title":"Notes"}' 3 >/dev/null || fail "map_create"
# One batch: the write gate refuses a page that is orphan or missing from its
# map's index (same shape as seed_index in test/smoke/http.sh).
mcp_call smoke concept_batch '{"operations":[{"op":"write","id":"notes/hello","index_entry":true,"frontmatter":{"type":"Index","title":"Hello"},"body":"# Hello\n\nImage smoke body. Overview: [[notes/overview]]\n"},{"op":"write","id":"notes/overview","index_entry":true,"frontmatter":{"type":"Index","title":"Overview"},"body":"# Overview\n\nSee [[notes/hello]].\n"}]}' 4 >/dev/null \
  || fail "concept_batch"
read_back=$(mcp_call smoke concept_read '{"id":"notes/hello"}' 5) || fail "concept_read"
grep -q 'Image smoke body' <<<"$read_back" || fail "concept_read does not return the written body: $read_back"
after=$(docker exec "$name" git -C /data/smoke rev-list --count HEAD)
[ "$after" -gt "$before" ] || fail "the write left no commit in /data/smoke ($before -> $after)"

echo "smoke: ok ($image)"
