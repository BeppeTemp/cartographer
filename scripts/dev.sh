#!/usr/bin/env bash
# Live development instance for a live session (the live-dev skill): the server
# rebuilt and restarted on every Go change, and the Atlas UI served by Vite with
# hot reload against it. Run from the worktree being edited: `make dev`.
#
# DATA is a data directory (one subdirectory per KB). It is copied into a
# temporary directory with every git remote removed (a KB syncs exactly when it
# has one), so what the session writes never leaves the machine and the source
# is never touched.
# Without DATA it runs on a throwaway copy of the generated test KBs.
#
# Polling rather than a file watcher: no watcher is a dependency of this
# repository, and `find -newer` once a second costs nothing at this size.
set -euo pipefail

API_PORT=${API_PORT:-39280}
UI_PORT=${UI_PORT:-5173}
# Resolved before the cd below, so a relative DATA means relative to the caller.
if [ -n "${DATA:-}" ]; then
	test -d "$DATA" || { echo "dev: DATA=$DATA is not a directory" >&2; exit 1; }
	DATA=$(cd "$DATA" && pwd -P)
fi
root=$(git rev-parse --show-toplevel)
cd "$root"

pid=
vite=
work=$(mktemp -d "${TMPDIR:-/tmp}/cartographer-dev.XXXXXX")
stop() {
	if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; fi
	if [ -n "$vite" ]; then kill "$vite" 2>/dev/null || true; fi
	rm -rf "$work"
}
# Installed before anything can fail: every early exit would otherwise leave a
# full copy of the KB data behind in the temp directory.
trap stop EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [ -n "${DATA:-}" ]; then
	# The contents, not the path: a DATA that is a symlink must not be copied as
	# a link, or the server and the remote removal below would act on the source.
	mkdir "$work/data"
	cp -R "$DATA/." "$work/data/"
	find "$work/data" -name .git -prune -print | while read -r g; do
		repo=$(dirname "$g")
		# A .git file points at a repository elsewhere (a worktree, a submodule):
		# removing its remotes would edit that repository's shared config.
		if [ ! -d "$g" ]; then
			echo "dev: $repo is a linked checkout (.git is a file); copy a standalone clone instead" >&2
			exit 1
		fi
		for r in $(git -C "$repo" remote); do git -C "$repo" remote remove "$r"; done
	done
	kbargs=(--data "$work/data")
else
	# The test KBs (internal/testkbs, D818): ten synthetic KBs from 5,000
	# concepts down to 30, built to put every Atlas view in a state worth
	# judging. Generating them takes about half a minute, so they are cached
	# per generator source and per day (their dates are relative to today).
	key=$( (cat internal/testkbs/*.go; date -u +%F) | shasum | cut -c1-12)
	caches="${TMPDIR:-/tmp}/cartographer-testkbs"
	cache="$caches/$key"
	if [ ! -d "$cache" ]; then
		echo "[dev] generating the test KBs (once a day)…"
		# Other worktrees may be reading their own cache right now: only
		# entries older than two days go, and the new one is generated
		# under a name of its own and renamed into place.
		mkdir -p "$caches"
		find "$caches" -mindepth 1 -maxdepth 1 -mtime +2 -exec rm -rf {} +
		tmp="$cache.tmp.$$"
		go run ./internal/testkbs/gen "$tmp"
		mv "$tmp" "$cache" 2>/dev/null || rm -rf "$tmp" # another session won the race
	fi
	mkdir "$work/data"
	cp -R "$cache/." "$work/data/"
	kbargs=(--data "$work/data")
fi

[ -d web/node_modules ] || (cd web && npm ci --silent)

stamp="$work/stamp"
start() {
	touch "$stamp"
	if ! go build -o "$work/cartographer" ./cmd/cartographer; then
		if [ -z "$pid" ]; then echo "[dev] build failed" >&2; exit 1; fi
		echo "[dev] build failed $(date +%T): the previous server keeps running" >&2
		return
	fi
	if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
	# The port must be free once our previous server is gone. A server that fails
	# to bind logs it and stays up, and the health check below would then be
	# answered by whatever holds the port: another session's instance, another KB.
	if curl -s -o /dev/null "http://127.0.0.1:$API_PORT/health"; then
		echo "[dev] 127.0.0.1:$API_PORT is taken: pick another API_PORT" >&2
		exit 1
	fi
	# Loopback only and no tokens: the instance holds a private copy, not a
	# shared server. An exported CARTOGRAPHER_TOKENS would otherwise apply here,
	# and the UI's API calls would answer 401.
	env -u CARTOGRAPHER_TOKENS CARTOGRAPHER_AUDIT_LOG="$work/audit.jsonl" "$work/cartographer" serve "${kbargs[@]}" \
		--http "127.0.0.1:$API_PORT" --update-check=false &
	pid=$!
	for _ in $(seq 1 30); do
		kill -0 "$pid" 2>/dev/null || { echo "[dev] the server exited: its log is above" >&2; exit 1; }
		curl -sf "http://127.0.0.1:$API_PORT/health" >/dev/null 2>&1 && break
		sleep 1
	done
	echo "[dev] server up on 127.0.0.1:$API_PORT (pid $pid) $(date +%T)"
}

start
# exec, so the pid is Vite's own and stop() reaches it (npx would leave it orphaned).
(cd web && CARTOGRAPHER_DEV_API="http://127.0.0.1:$API_PORT" exec node_modules/.bin/vite --port "$UI_PORT" --strictPort) &
vite=$!
echo "[dev] UI on http://localhost:$UI_PORT/ui/ — Ctrl-C stops both"

# What the binary is built from: Go sources (not tests: an implementer editing
# a test must not restart the server under the user) and the embedded files.
while sleep 1; do
	kill -0 "$vite" 2>/dev/null || { echo "[dev] Vite exited: is $UI_PORT taken? (UI_PORT=…)" >&2; exit 1; }
	if [ -n "$(find cmd internal go.mod go.sum -newer "$stamp" \( \
		\( -name '*.go' ! -name '*_test.go' \) -o -name 'go.*' \
		-o -path 'internal/skillbundle/bundled/*' -o -path 'internal/webui/dist/*' \) \
		-type f -print -quit)" ]; then
		start
	fi
done
