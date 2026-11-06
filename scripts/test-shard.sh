#!/usr/bin/env bash
# Runs one shard of `go test ./...`: shard INDEX (0-based) of COUNT.
#
#   test-shard.sh COUNT INDEX [PKG...]   one shard, of PKG... instead of ./...
#   test-shard.sh COUNT all [PKG...]     every shard at once, in one call: the Windows
#                                        leg of make signoff (one machine, one lock)
#
# Used by the `test-windows` CI job, where the git-backed packages are ~13x
# slower than on Linux and the whole job used to wait on internal/mcpserver
# alone (D785). Every package's top-level tests are dealt
# round-robin, so each shard gets a slice of every slow package; together the
# shards run each test exactly once. The regex is per package because one
# regex over the whole repository exceeds Windows' command-line limit.
#
# internal/repodocs is skipped: it checks the repository's documents, not
# platform behaviour, and the ubuntu `test` job already runs it.
set -euo pipefail

count=${1:?usage: test-shard.sh COUNT INDEX|all [PKG...]}
index=${2:?usage: test-shard.sh COUNT INDEX|all [PKG...]}
shift 2
logs=$(mktemp -d)
trap 'rm -rf "$logs"' EXIT

if [ "$index" = all ]; then
	# The test binaries build once, here, rather than in every shard at the same time.
	# shellcheck disable=SC2046
	go test -list '.*' $(go list "${@:-./...}" | grep -v '/internal/repodocs$') >/dev/null 2>&1 || true
	# Each shard's log is printed whole after it ends, so the shards do not interleave.
	pids=()
	for ((i = 0; i < count; i++)); do
		"$0" "$count" "$i" "$@" >"$logs/shard-$i.log" 2>&1 &
		pids+=("$!")
	done
	failed=0
	for i in "${!pids[@]}"; do
		wait "${pids[$i]}" || failed=1
		cat "$logs/shard-$i.log"
	done
	exit "$failed"
fi

# One `go test -list` over every package, so the test binaries build in
# parallel; each package's names come before its `ok <pkg>` line, in the
# same order on every shard.
# shellcheck disable=SC2046  # the package list is a word list on purpose
go test -list '.*' $(go list "${@:-./...}" | grep -v '/internal/repodocs$') >"$logs/list"
awk -v n="$count" -v i="$index" '
	/^(Test|Example|Fuzz)/ { names[++k] = $1; next }
	/^ok/ {
		re = ""
		for (j = 1; j <= k; j++) if ((j - 1) % n == i) re = re (re == "" ? "" : "|") names[j]
		if (re != "") print $2, "^(" re ")$"
		delete names; k = 0
	}' "$logs/list" >"$logs/plan"

pids=()
while read -r pkg re; do
	# 20m as in `make test`: the timeout is per package binary.
	go test -count=1 -timeout 20m -run "$re" "$pkg" >"$logs/${#pids[@]}.log" 2>&1 &
	pids+=("$!")
done <"$logs/plan"

failed=0
for i in "${!pids[@]}"; do
	if ! wait "${pids[$i]}"; then
		failed=1
	fi
	cat "$logs/$i.log"
done
exit "$failed"
