#!/usr/bin/env bash
# ac-trace.sh — trace a change's acceptance criteria to the tests that cite them (advisory).
#
#   ac-trace.sh                    the AC: lines of TRIAGE_BODY against the test files at HEAD
#                                  (scripts/pr-triage.sh runs it in its branch mode)
#   ac-trace.sh --parse            the AC: lines of the message on stdin, one row each:
#                                  ok<TAB>id<TAB>kind<TAB>sentence, or bad<TAB><the line>
#   ac-trace.sh --citations <rev>  every criterion id a test file cites at <rev>: id<TAB>path:line
#
# A criterion travels in the change's message as `AC: <id> <kind> <sentence>` at column 0
# (CONTRIBUTING.md §Plans), anywhere in the message: a line that is no trailer in the same
# paragraph, such as `Closes #12`, hides the block from `git interpret-trailers`. The id is
# AC-<plan id>-<n>, the kind test, manual or deferred. A `test` criterion is traced when its
# id appears, as a whole word (AC-P1-1 is not AC-P1-10 nor AC-P1-1-2), in a file kind() of
# pr-triage.sh calls `test`. No AC: line prints nothing. Otherwise one `Criteria:` line, then
# one WARN per problem; never a BLOCK, always exit 0 when it ran (make ready greps ^BLOCK).
# Exit 2 with a message on stderr when it cannot run: pr-triage.sh then says the check did not run.
set -euo pipefail

here=$(dirname "$0")
# The one classifier of paths is kind() of pr-triage.sh: read from there, never copied, so
# what counts as a test here cannot drift from the triage's size split.
eval "$(sed -n '/^kind() {/,/^}/p' "$here/pr-triage.sh")"
[ "$(type -t kind)" = function ] || { echo "ac-trace.sh: no kind() in $here/pr-triage.sh" >&2; exit 2; }

ac_re='^AC:[[:space:]]+(AC-[A-Za-z0-9]+(-[A-Za-z0-9]+)*-[0-9]+)[[:space:]]+(test|manual|deferred)[[:space:]]+([^[:space:]].*)$'

parse() {
  local line
  while IFS= read -r line || [ -n "$line" ]; do
    line=${line%$'\r'}
    case "$line" in AC:*) ;; *) continue ;; esac
    line=${line%"${line##*[![:space:]]}"}
    if [[ "$line" =~ $ac_re ]]; then
      printf 'ok\t%s\t%s\t%s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[3]}" "${BASH_REMATCH[4]}"
    else
      printf 'bad\t%s\n' "$line"
    fi
  done
}

# A token is maximal over [A-Za-z0-9-] (the character before it is none of those, and the
# regexp is greedy), so comparing whole tokens is the whole-word match.
citations() {
  local rev=$1 path lineno match rc=0 out
  git rev-parse -q --verify "$rev^{commit}" >/dev/null || { echo "ac-trace.sh: $rev is not a commit" >&2; return 2; }
  # -z: the path verbatim, then NUL, the line number, NUL, the match; exit 1 is "no match".
  out=$(git -C "$(git rev-parse --show-toplevel)" grep -z -n -o -I -E \
    '(^|[^A-Za-z0-9_-])AC-[A-Za-z0-9]+(-[A-Za-z0-9]+)*' "$rev" -- . | tr '\0' '\t') || rc=$?
  [ "$rc" -le 1 ] || { echo "ac-trace.sh: git grep failed on $rev" >&2; return 2; }
  [ -n "$out" ] || return 0
  while IFS=$'\t' read -r path lineno match; do
    path=${path#"$rev":}
    match=${match#"${match%%AC-*}"}
    [ "$(kind "$path")" = test ] || continue
    printf '%s\t%s:%s\n' "$match" "$path" "$lineno"
  done <<<"$out"
}

case "${1:-}" in
  --parse) parse; exit 0 ;;
  --citations) [ $# -eq 2 ] || { echo "usage: ac-trace.sh --citations <rev>" >&2; exit 2; }; citations "$2"; exit ;;
  '') ;;
  *) echo "usage: ac-trace.sh [--parse | --citations <rev>]" >&2; exit 2 ;;
esac

rows=$(printf '%s\n' "${TRIAGE_BODY:-}" | parse)
[ -n "$rows" ] || exit 0
cited=$(citations HEAD | cut -f1 | sort -u) || exit 2

seen="" warns="" n=0 traced=0 manual=0 deferred=0
while IFS=$'\t' read -r st rest; do
  if [ "$st" = bad ]; then
    warns="${warns}WARN: unreadable AC line: $rest"$'\n'
    continue
  fi
  IFS=$'\t' read -r id kind _ <<<"$rest"
  if grep -qxF -- "$id" <<<"$seen"; then
    warns="${warns}WARN: $id is declared twice — keep one AC: line per criterion"$'\n'
    continue
  fi
  seen="$seen$id"$'\n'
  n=$((n + 1))
  case "$kind" in
    manual) manual=$((manual + 1)) ;;
    deferred) deferred=$((deferred + 1)) ;;
    test)
      if grep -qxF -- "$id" <<<"$cited"; then
        traced=$((traced + 1))
      else
        warns="${warns}WARN: $id has no test citing it — add the id in a comment on the test, or mark it manual or deferred"$'\n'
      fi ;;
  esac
done <<<"$rows"

echo "Criteria: $n declared · $traced traced · $manual manual · $deferred deferred"
printf '%s' "$warns"
exit 0
