#!/usr/bin/env bash
# Live development instance for a live session (the live-dev skill): the server
# rebuilt and restarted on every Go change, and the Atlas UI served by Vite with
# hot reload against it. Run from the worktree being edited: `make dev`.
#
# DATA is a data directory (one subdirectory per KB). It is copied into a
# temporary directory with every git remote removed and git sync off, so what
# the session writes never leaves the machine and the source is never touched.
# Without DATA the server runs on a throwaway demo KB.
#
# Polling rather than a file watcher: no watcher is a dependency of this
# repository, and `find -newer` once a second costs nothing at this size.
set -euo pipefail

API_PORT=${API_PORT:-39280}
UI_PORT=${UI_PORT:-5173}
root=$(git rev-parse --show-toplevel)
cd "$root"

work=$(mktemp -d "${TMPDIR:-/tmp}/cartographer-dev.XXXXXX")
if [ -n "${DATA:-}" ]; then
	test -d "$DATA" || { echo "dev: DATA=$DATA is not a directory" >&2; exit 1; }
	cp -R "$DATA" "$work/data"
	find "$work/data" -name .git -prune -print | while read -r g; do
		repo=$(dirname "$g")
		for r in $(git -C "$repo" remote); do git -C "$repo" remote remove "$r"; done
	done
	kbargs=(--data "$work/data")
else
	kbargs=(--kb "$work/demo-kb" --init)
fi

[ -d web/node_modules ] || (cd web && npm ci --silent)

stamp="$work/stamp"
pid=
vite=
stop() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null
	[ -n "$vite" ] && kill "$vite" 2>/dev/null
	rm -rf "$work"
}
trap 'stop; exit' INT TERM
trap stop EXIT

start() {
	touch "$stamp"
	if ! go build -o "$work/cartographer" ./cmd/cartographer; then
		echo "[dev] build failed $(date +%T): the previous server keeps running" >&2
		return
	fi
	if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
	# Loopback only and no tokens: the instance holds a private copy, not a
	# shared server. An exported CARTOGRAPHER_TOKENS would otherwise apply here.
	env -u CARTOGRAPHER_TOKENS "$work/cartographer" serve "${kbargs[@]}" \
		--http "127.0.0.1:$API_PORT" --git-sync=false --update-check=false &
	pid=$!
	echo "[dev] server up on 127.0.0.1:$API_PORT (pid $pid) $(date +%T)"
}

start
# exec, so the pid is Vite's own and stop() reaches it (npx would leave it orphaned).
(cd web && CARTOGRAPHER_DEV_API="http://127.0.0.1:$API_PORT" exec node_modules/.bin/vite --port "$UI_PORT" --strictPort) &
vite=$!
echo "[dev] UI on http://localhost:$UI_PORT/ui/ — Ctrl-C stops both"

while sleep 1; do
	if [ -n "$(find cmd internal go.mod go.sum \( -name '*.go' -o -name 'go.*' \) -newer "$stamp" -print -quit)" ]; then
		start
	fi
done
