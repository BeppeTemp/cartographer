#!/usr/bin/env bash
# Runs one shard of `go test ./...`: shard INDEX (0-based) of COUNT.
#
# Used by the `test-windows` CI job, where the git-backed packages are ~13x
# slower than on Linux and the whole job used to wait on internal/mcpserver
# alone (D778). Every package's top-level tests are sorted and dealt
# round-robin, so each shard gets a slice of every slow package; together the
# shards run each test exactly once. The regex is per package because one
# regex over the whole repository exceeds Windows' command-line limit.
#
# internal/repodocs is skipped: it checks the repository's documents, not
# platform behaviour, and the ubuntu `test` job already runs it.
set -euo pipefail

count=${1:?usage: test-shard.sh COUNT INDEX}
index=${2:?usage: test-shard.sh COUNT INDEX}
logs=$(mktemp -d)
trap 'rm -rf "$logs"' EXIT

# One `go test -list` over every package, so the test binaries build in
# parallel; each package's names come before its `ok <pkg>` line, in the
# same order on every shard.
go test -list '.*' $(go list ./... | grep -v '/internal/repodocs$') >"$logs/list"
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
