#!/usr/bin/env bash
# Smoke test of the Surveyor image (D1440): the tools the `surveyor` stage
# promises (git and the CA bundle), the tools it must not carry (sops, age, ssh),
# a non-root user and the baked entrypoint. Needs no volume and no network, on
# the runner's own architecture. Exits non-zero on any deviation.
#
# Usage: test/smoke/surveyor-image.sh <image> [expected-version]   (or `make docker-surveyor-smoke`)
set -euo pipefail

image=${1:?usage: test/smoke/surveyor-image.sh <image> [expected-version]}
want_version=${2:-}

fail() { echo "surveyor smoke: $1" >&2; exit 1; }

echo "→ tools in the image"
docker run --rm --entrypoint sh "$image" -c 'git --version' >/dev/null 2>&1 \
  || fail "git is missing from the image"
docker run --rm --entrypoint sh "$image" -c 'test -x "$(git --exec-path)/git-remote-https"' >/dev/null 2>&1 \
  || fail "git-remote-https is missing: git cannot clone over https"
docker run --rm --entrypoint sh "$image" -c 'test -s /etc/ssl/certs/ca-certificates.crt' >/dev/null 2>&1 \
  || fail "the CA bundle /etc/ssl/certs/ca-certificates.crt is missing or empty"

echo "→ tools that must be absent"
for tool in sops age ssh; do
  if docker run --rm --entrypoint sh "$image" -c "command -v $tool" >/dev/null 2>&1; then
    fail "$tool is in the image: the Surveyor image carries none of the server's secret or ssh tooling"
  fi
done

echo "→ user and entrypoint"
uid=$(docker run --rm --entrypoint id "$image" -u)
[ "$uid" != 0 ] || fail "the image runs as root"
ep=$(docker image inspect --format '{{join .Config.Entrypoint " "}}' "$image")
[ "$ep" = "cartographer surveyor" ] || fail "entrypoint is '$ep', want 'cartographer surveyor'"
label=$(docker image inspect --format '{{index .Config.Labels "io.modelcontextprotocol.server.name"}}' "$image")
[ -z "$label" ] || fail "the image carries the MCP Registry label '$label': it belongs to the server image"

# The entrypoint swallows 'version' as a Surveyor argument: --entrypoint it is.
version=$(docker run --rm --entrypoint cartographer "$image" version) || fail "'version' failed"
echo "  $version"
if [ -n "$want_version" ]; then
  case $version in *"$want_version"*) ;; *) fail "version '$version' does not carry the VERSION build-arg '$want_version'" ;; esac
fi

echo "→ the baked entrypoint hands arguments to the Surveyor"
rc=0
docker run --rm "$image" >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "no arguments exited $rc, want 2 (usage, nothing started)"
out=$(docker run --rm "$image" source help 2>&1) || fail "'source help' failed"
case $out in *"source test"*) ;; *) fail "'source help' does not print the source test usage" ;; esac

echo "surveyor smoke: ok ($image)"
