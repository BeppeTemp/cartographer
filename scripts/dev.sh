#!/usr/bin/env bash
# Live development instance for a live session (the cartographer-develop-live skill): the server
# rebuilt and restarted on every Go change, and the Atlas UI served by Vite with
# hot reload against it. Run from the worktree being edited: `make dev`.
#
# DATA is a data directory (one subdirectory per KB). It is copied into a
# temporary directory where every git remote keeps its fetch URL, so the sync
# badge shows the KB's real state against its remote, and gets a push URL
# that cannot be reached, so what the session writes never leaves the machine
# (a push fails, and the badge shows that failure too). The source is never
# touched.
# The generated test KBs (internal/testkbs) are always mounted: alone without
# DATA, beside the copied KBs with it (a test KB whose name a DATA KB already
# has is left out), so every Atlas view has something to show whatever the data.
# A synthetic Surveyor reports on three of them (heartbeat, settings, runs and
# report candidates, posted to the component routes after every start and
# repeated every half hour, its declaration being live for three hours): the
# Surveyor views are never empty. Without DATA a synthetic client reports too;
# with it this machine's own client does.
#
# Polling rather than a file watcher: no watcher is a dependency of this
# repository, and `find -newer` once a second costs nothing at this size.
# SERVE_CONFIG is a server config.yaml read under the flags (they win): what
# --data cannot say, e.g. plugins_dir and plugins to try a branding plugin.
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
refresher=
work=$(mktemp -d "${TMPDIR:-/tmp}/cartographer-dev.XXXXXX")
stop() {
	if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; fi
	if [ -n "$vite" ]; then kill "$vite" 2>/dev/null || true; fi
	if [ -n "${refresher:-}" ]; then kill "$refresher" 2>/dev/null || true; fi
	rm -rf "$work"
}
# Installed before anything can fail: every early exit would otherwise leave a
# full copy of the KB data behind in the temp directory.
trap stop EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# The test KBs (internal/testkbs, D818, D1032): eleven synthetic KBs, the
# 5,000-concept scale KB, nine shaped after measured real KBs with invented
# content, and a bare 30, built to put every Atlas view in a state worth
# judging. Generating them takes a few seconds, so they are cached
# per generator source and per day (their dates are relative to today).
# The client report and the Surveyor bodies are built from the client and
# Surveyor schemas and the component protocol, so the sources of those are
# part of the key too.
# add_test_kbs copies them into $1, skipping a name already there, and copies the
# files beside them (the client report, the Surveyor bodies), which are not KBs
# and which start() posts to every server it starts, into $work.
add_test_kbs() {
	local key caches cache tmp entry name
	key=$( (cat internal/testkbs/*.go; find internal/clientconfig internal/settingschema internal/surveyorconfig internal/componentproto -name '*.go' ! -name '*_test.go' | sort | xargs cat; date -u +%F) | shasum | cut -c1-12)
	# Under the user's own cache directory, mode 700 (scripts/testkbs-cache-dir.sh).
	caches=$(scripts/testkbs-cache-dir.sh)
	cache="$caches/$key"
	if [ ! -d "$cache" ]; then
		echo "[dev] generating the test KBs (once a day)…"
		# Other worktrees may be reading their own cache right now: only
		# entries older than two days go, and the new one is generated
		# under a name of its own and renamed into place.
		find "$caches" -mindepth 1 -maxdepth 1 -mtime +2 -exec rm -rf {} +
		tmp="$cache.tmp.$$"
		go run ./internal/testkbs/gen "$tmp"
		mv "$tmp" "$cache" 2>/dev/null || rm -rf "$tmp" # another session won the race
	fi
	for entry in "$cache"/*; do
		name=$(basename "$entry")
		if [ -d "$entry" ]; then
			if [ -e "$1/$name" ]; then
				echo "[dev] the test KB $name is left out: a KB of DATA has the name" >&2
				continue
			fi
			cp -R "$entry" "$1/$name"
		else
			cp "$entry" "$work/$name"
		fi
	done
}

mkdir "$work/data"
if [ -n "${DATA:-}" ]; then
	# The contents, not the path: a DATA that is a symlink must not be copied as
	# a link, or the server and the push URL change below would act on the source.
	cp -R "$DATA/." "$work/data/"
	find "$work/data" -name .git -prune -print | while read -r g; do
		repo=$(dirname "$g")
		# A .git file points at a repository elsewhere (a worktree, a submodule):
		# changing its push URL would edit that repository's shared config.
		if [ ! -d "$g" ]; then
			echo "dev: $repo is a linked checkout (.git is a file); copy a standalone clone instead" >&2
			exit 1
		fi
		for r in $(git -C "$repo" remote); do git -C "$repo" remote set-url --push "$r" "$work/no-push"; done
	done
fi
add_test_kbs "$work/data"
kbargs=(--data "$work/data")

[ -d web/node_modules ] || (cd web && npm ci --silent)

# The server keeps the client settings reports in memory (D818), so a restart
# forgets the test client: every start tells the server about it again, as a
# client does with `cartographer sync`. The test KBs come with a report; with
# DATA the instance holds copies of this machine's KBs, so this machine's own
# client reports to it (`sync --report-to`: the config is read, never saved,
# nothing is provisioned and the configured server is not contacted).
post_client_report() {
	if [ -n "${DATA:-}" ]; then
		for _ in $(seq 1 30); do
			"$work/cartographer" sync --report-to "http://127.0.0.1:$API_PORT/mcp" 2>/dev/null && return 0
			sleep 1
		done
		echo "[dev] this machine's client did not report: no client config, or the server refused it" >&2
		return 0
	fi
	[ -f "$work/client-report.json" ] || return 0
	# /health answers before the KBs are open, and the route refuses until then.
	for _ in $(seq 1 30); do
		curl -sf -X POST -H 'Content-Type: application/json' --data-binary @"$work/client-report.json" \
			"http://127.0.0.1:$API_PORT/api/client-settings" >/dev/null && return 0
		sleep 1
	done
	echo "[dev] the client report was refused" >&2
}

# The synthetic Surveyor (internal/testkbs/surveyor.go): what it posts, in the
# order a Surveyor does, to the component routes. The server runs without tokens,
# so the local principal is let through, and the heartbeat comes first because
# the settings and the runs hang on a live declaration of the same perimeter.
# It is repeated by surveyor_refresh: a declaration is live for three intervals
# and the beat interval is capped at an hour.
post_surveyor() {
	local f="$work/surveyor-heartbeat.json"
	[ -f "$f" ] || return 0
	local base="http://127.0.0.1:$API_PORT/api/component/v1" n
	for _ in $(seq 1 30); do
		curl -sf -X POST -H 'Content-Type: application/json' --data-binary @"$f" "$base/heartbeat" >/dev/null && break
		sleep 1
	done
	for n in settings runs; do
		curl -sf -X POST -H 'Content-Type: application/json' --data-binary @"$work/surveyor-$n.json" "$base/$n" >/dev/null ||
			echo "[dev] the synthetic Surveyor's $n were refused" >&2
	done
}
surveyor_refresh() {
	[ -f "$work/surveyor-heartbeat.json" ] || return 0
	[ -z "$refresher" ] || return 0
	(while sleep 1800; do post_surveyor; done) &
	refresher=$!
}

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
	env -u CARTOGRAPHER_TOKENS CARTOGRAPHER_AUDIT_LOG="$work/audit.jsonl" "$work/cartographer" serve "${kbargs[@]}" ${SERVE_CONFIG:+--config "$SERVE_CONFIG"} \
		--http "127.0.0.1:$API_PORT" --update-check=false &
	pid=$!
	for _ in $(seq 1 30); do
		kill -0 "$pid" 2>/dev/null || { echo "[dev] the server exited: its log is above" >&2; exit 1; }
		if curl -sf "http://127.0.0.1:$API_PORT/health" >/dev/null 2>&1; then
			post_client_report
			post_surveyor
			surveyor_refresh
			break
		fi
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
