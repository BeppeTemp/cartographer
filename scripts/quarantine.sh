#!/usr/bin/env bash
# quarantine.sh — the flaky Go tests taken out of the gate's verdict while they are fixed
# (test/quarantine.tsv, D2294).
#
#   quarantine.sh [--today DATE] MODE, the MODE one of:
#   quarantine.sh --skip [PKG...]  the -skip regular expression of the active entries, `^(A|B)$`,
#                                  only those of the packages PKG... (import paths) when given;
#                                  nothing when there is none
#   quarantine.sh --list           the active entries, as their lines of the list
#   quarantine.sh --count          their number
#   quarantine.sh --expired        the entries past their date, as their lines of the list
#   quarantine.sh --run            each active entry alone in its package, uncached, outside any
#                                  verdict: `quarantine: <pkg>.<Test> PASS|FAIL` per entry, then
#                                  `quarantine: <n> entries, <k> failed`; go test's output on stderr
#
# The list: tab-separated, blank lines and `#` comments ignored, one test per line, five fields:
#   <import path of the package> <top-level test function> <issue number> <added> <expires>
# the dates YYYY-MM-DD. An entry is active until its expiry, inclusive (UTC; `--today <date>` replaces
# the clock, for the tests only: an option, not an environment variable, so no exported value reaches
# a gate through make, the shards or the signoff): past it, it lapses and the test is back in the
# verdict without any change to the list. No entry may name a package or a test of internal/repodocs:
# its tests check the documents and the process, this list's own rules among them, and a quarantined
# name is skipped in every package, so such an entry would switch off its own validator. `-skip`
# matches a name in every package of a `go test` call, so a quarantined name is unique in the module;
# internal/repodocs/quarantine_test.go pins that on the real list, with the test's existence and the
# 30-day bound (static checks: no clock in a gate).
#
# Exit 0, also when a test of --run fails; 2 a bad call, a missing list, or a list that does not parse
# (its line number on stderr). Every caller fails on 2: a broken list must not silently skip nothing.
#
# The list is found beside this script, not through git: the build host's tree may have no .git.
# Plain bash 3.2 and POSIX awk (the macOS defaults); it also runs under Git for Windows' bash
# (scripts/test-shard.sh). No date arithmetic, so no GNU or BSD `date` flags: YYYY-MM-DD orders
# as a string.
set -euo pipefail

usage() { echo "usage: quarantine.sh [--today YYYY-MM-DD] --skip [PKG...] | --list | --count | --expired | --run" >&2; exit 2; }
today=$(date -u +%Y-%m-%d)
if [ "${1:-}" = --today ]; then
  [ $# -ge 2 ] || usage
  today=$2
  shift 2
fi
[ $# -gt 0 ] || usage
mode=$1
shift
case "$mode" in
  --skip) ;;
  --list | --count | --expired | --run) [ $# = 0 ] || usage ;;
  *) usage ;;
esac

here=$(cd "$(dirname "$0")" && pwd)
list=$here/../test/quarantine.tsv
[ -f "$list" ] && [ -r "$list" ] || { echo "quarantine: cannot read test/quarantine.tsv" >&2; exit 2; }
# The top-level tests of internal/repodocs and of its landertest subpackage, beside the list (none in a tree without it): " A B ".
pinned=" "
pins=("$here"/../internal/repodocs/*_test.go "$here"/../internal/repodocs/landertest/*_test.go)
if [ -e "${pins[0]}" ]; then
  pinned=$(awk '/^func Test[A-Za-z0-9_]*\(/ { sub(/^func /, ""); sub(/\(.*/, ""); s = s " " $0 } END { print s " " }' "${pins[@]}")
fi

# entries <active|expired>: those lines of the list, once every line has been checked.
entries() {
  tr -d '\r' <"$list" | awk -F '\t' -v today="$today" -v want="$1" -v pinned="$pinned" '
    function date_ok(d,   y, m, n, max) {
      if (d !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) return 0
      y = substr(d, 1, 4) + 0; m = substr(d, 6, 2) + 0; n = substr(d, 9, 2) + 0
      if (m < 1 || m > 12 || n < 1) return 0
      max = 31
      if (m == 4 || m == 6 || m == 9 || m == 11) max = 30
      if (m == 2) max = ((y % 4 == 0 && y % 100 != 0) || y % 400 == 0) ? 29 : 28
      return n <= max
    }
    function bad(why) { printf "quarantine: test/quarantine.tsv:%d: %s\n", NR, why | "cat 1>&2"; failed = 1 }
    BEGIN {
      if (!date_ok(today)) { print "quarantine: today is not a YYYY-MM-DD date: " today | "cat 1>&2"; failed = 1; exit }
    }
    /^[ \t]*(#|$)/ { next }
    {
      if (NF != 5) { bad("want five tab-separated fields, got " NF); next }
      if ($1 !~ /^[A-Za-z0-9_.~-]+(\/[A-Za-z0-9_.~-]+)*$/) { bad("not an import path: " $1); next }
      if ($2 !~ /^Test([A-Z0-9_][A-Za-z0-9_]*)?$/) { bad("not a Go test function name: " $2); next }
      if ($3 !~ /^[1-9][0-9]*$/) { bad("not an issue number: " $3); next }
      if (!date_ok($4)) { bad("added is not a YYYY-MM-DD date: " $4); next }
      if (!date_ok($5)) { bad("expires is not a YYYY-MM-DD date: " $5); next }
      if ($1 ~ /(^|\/)internal\/repodocs(\/|$)/) { bad("internal/repodocs checks the documents and the process: its tests cannot be quarantined"); next }
      if (index(pinned, " " $2 " ")) { bad($2 " is a test of internal/repodocs, which cannot be quarantined"); next }
      if ($2 in seen) { bad($2 " is already on line " seen[$2]); next }
      seen[$2] = NR
      # YYYY-MM-DD compares as a string; active through the expiry day itself.
      if ((want == "active") == (today "" <= $5 "")) out[++n] = $0
    }
    END {
      close("cat 1>&2")
      if (failed) exit 2
      for (i = 1; i <= n; i++) print out[i]
    }'
}

case "$mode" in
  --skip)
    entries active | awk -F '\t' -v pkgs=" $* " '
      pkgs == "  " || index(pkgs, " " $1 " ") { re = re (re == "" ? "" : "|") $2 }
      END { if (re != "") print "^(" re ")$" }' ;;
  --list) entries active ;;
  --expired) entries expired ;;
  --count) entries active | awk 'END { print NR }' ;;
  --run)
    active=$(entries active)
    cd "$here/.."
    n=0 k=0
    while IFS=$'\t' read -r pkg name _; do
      [ -n "$pkg" ] || continue
      n=$((n + 1))
      if go test -count=1 -timeout 20m -run "^$name\$" "$pkg" >&2; then r=PASS; else r=FAIL; k=$((k + 1)); fi
      echo "quarantine: $pkg.$name $r"
    done <<<"$active"
    echo "quarantine: $n entries, $k failed" ;;
esac
