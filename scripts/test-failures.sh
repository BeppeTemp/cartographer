#!/usr/bin/env bash
# test-failures.sh — what a red gate log says failed, for the targeted retry of
# scripts/signoff.sh (D1755).
#
#   test-failures.sh <log>   ->  one "<race> <package> <Test>" line per failed top-level Go test
#
# <race> is 1 for a test that failed under the race detector (after the `race`
# target's `go test -race` line, until the next `go test` line), else 0. The log is a
# `make` run of the signoff's targets, or the Windows leg's `go test` output (no make lines).
#
# Exit 0: every failure the log shows is one of the printed tests, so running their
# packages once more covers it. Exit 3: something failed that such a retry would not
# cover — a make target other than `test` or `race` (fmt, vet, smoke, e2e, the web legs),
# a make error of its own, a package that did not build or set up, a panic (a test
# timeout is one), a data race report, a package FAIL line with no named test before it.
# Exit 4: no failure found.
#
# The log holds the tests' own output too, so a line is taken as go test's only in go
# test's exact shape: a top-level `--- FAIL: <Test> (<n>s)` with a Go test function name,
# a `FAIL<TAB><package><TAB><n>s` with an import path. Any other line that starts like
# one is an exit 3: nothing the signoff runs again comes from a line it could not read.
#
# Plain bash 3.2 and POSIX awk (the macOS defaults).
set -euo pipefail

log=${1:?usage: test-failures.sh <log>}
[ -r "$log" ] || { echo "test-failures: cannot read $log" >&2; exit 2; }

esc=$(printf '\033')
sed "s/${esc}\[[0-9;]*m//g" "$log" | tr -d '\r' | awk '
  # A make target that failed: GNU make 4 "make: *** [Makefile:23: test] Error 1",
  # 3.81 "make: *** [test] Error 1", sub-makes "make[1]: ...". A make error with no
  # target in brackets ("*** No rule to make target") is a failure of make itself.
  /^make(\[[0-9]+\])?: \*\*\* / {
    t = $0
    if (t !~ /\*\*\* \[/) other = 1
    sub(/^.*\*\*\* \[/, "", t); sub(/].*$/, "", t); sub(/^.*: /, "", t)
    if (t != "test" && t != "race") other = 1
    failed = 1; next
  }
  /^go test / { race = ($0 ~ /^go test -race /) }
  /^panic: / || /^WARNING: DATA RACE/ { bad = 1 }
  /^--- FAIL: / {
    n = $3; sub(/\/.*$/, "", n)
    if (NF != 4 || $4 !~ /^\([0-9.]+s\)$/ || n !~ /^(Test|Example|Fuzz|Benchmark)[A-Za-z0-9_]*$/) { bad = 1; next }
    if (!(n in pend)) { pend[n] = 1; np++ }
    next
  }
  /^FAIL\t/ {
    failed = 1
    if ($0 ~ /\[(build|setup) failed\]/ || np == 0 || NF != 3 || $3 !~ /^[0-9.]+s$/ ||
        $2 !~ /^[A-Za-z0-9_][A-Za-z0-9_.\/-]*$/) bad = 1
    else for (n in pend) print race + 0, $2, n
    for (n in pend) delete pend[n]
    np = 0; next
  }
  END {
    if (np > 0) bad = 1
    if (other || bad) exit 3
    if (!failed) exit 4
  }'
