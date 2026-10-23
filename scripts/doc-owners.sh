#!/usr/bin/env bash
# doc-owners.sh — warn when a path mapped in doc-owners.tsv changed and its docs page did not.
#
#   doc-owners.sh < classified-rows      kind<TAB>added<TAB>deleted<TAB>path, as pr-triage.sh builds them
#
# Env: DOCS_PR_BODY  the PR body ("" before a PR exists); a line "Docs: none - <reason>"
#                    silences every hit and is echoed back
#      DOC_OWNERS_TSV the map (default: doc-owners.tsv next to this script; tests override it)
# A hit is a row whose glob has DOC_FLOOR or more production lines changed (added +
# deleted) while its page is absent from the changed paths. Advisory: it prints, never
# blocks. Exit 2 with a message on stderr when the map is unreadable or malformed.
# It reads data only: nothing it reads is sourced, evaluated or executed.
set -euo pipefail

# The floor is CONTRIBUTING.md §Pull requests; change it there and here together.
DOC_FLOOR=10
tsv=${DOC_OWNERS_TSV:-$(dirname "$0")/doc-owners.tsv}
body=${DOCS_PR_BODY:-}

[ -r "$tsv" ] || { echo "doc-owners.sh: cannot read $tsv" >&2; exit 2; }
rows=$(cat)

hits=""
n=0
lineno=0
while IFS= read -r line || [ -n "$line" ]; do
  lineno=$((lineno + 1))
  case "$line" in ''|'#'*) continue ;; esac
  IFS=$'\t' read -r glob page why extra <<<"$line"
  if [ -z "${glob:-}" ] || [ -z "${page:-}" ] || [ -z "${why:-}" ] || [ -n "${extra:-}" ]; then
    echo "doc-owners.sh: $tsv:$lineno: a row needs exactly three non-empty tab-separated fields" >&2
    exit 2
  fi
  a=0; d=0; touched=""
  while IFS=$'\t' read -r kind add del path; do
    [ -n "$path" ] || continue
    [ "$path" = "$page" ] && touched=1
    [ "$kind" = production ] || continue
    # shellcheck disable=SC2254 # the glob is a pattern on purpose
    case "$path" in $glob) a=$((a + add)); d=$((d + del)) ;; esac
  done <<<"$rows"
  if [ $((a + d)) -ge "$DOC_FLOOR" ] && [ -z "$touched" ]; then
    n=$((n + 1))
    hits="$hits"$(printf 'WARN: %s changed (+%s/-%s production lines) but %s not touched (docs/index.md §Documentation maintenance rules, row: %s); update the page, or put "Docs: none - <reason>" in the PR body' "$glob" "$a" "$d" "$page" "$why")$'\n'
  fi
done <"$tsv"

[ "$n" -gt 0 ] || exit 0
reason=$(printf '%s\n' "$body" | tr -d '\r' | grep -i -m1 '^Docs: none - .\+' || true)
if [ -n "$reason" ]; then
  echo "$reason ($n docs-owner warnings silenced)"
else
  printf '%s' "$hits"
fi
