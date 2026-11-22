#!/usr/bin/env bash
# ac-report.sh — the acceptance criteria landed in a range, as a markdown table (read-only).
#
#   ac-report.sh [<range>]   default: from the newest stable tag (v*, not a -rc. candidate)
#                            reachable from origin/main to origin/main; all of origin/main
#                            when there is none. It does not fetch: fetch first.
#
# One row per criterion id the `AC:` lines of the range's commits declare (scripts/ac-trace.sh
# --parse reads them, CONTRIBUTING.md §Plans), `manual` rows first, then `test`, then
# `deferred`, each group in landing order: the id, the kind and the sentence of its first
# landing, every change that declared it (its Lander-Slug trailer, else the short sha) and, for
# a `test` row, the file:line of a test citing the id at the range's end, or `not found`. A
# landed line that does not parse is listed after the table as `unreadable:`, with its commit.
# The `manual` rows are the input of a release candidate's test matrix: a plan's criteria are
# written once, and outlive the plan.
set -euo pipefail

here=$(dirname "$0")
if [ $# -ge 1 ]; then
  range=$1
else
  git rev-parse -q --verify 'origin/main^{commit}' >/dev/null || { echo "ac-report.sh: no origin/main; fetch it, or name a range" >&2; exit 2; }
  tag=$(git describe --tags --abbrev=0 --match 'v*' --exclude '*-rc.*' origin/main 2>/dev/null || true)
  range=${tag:+$tag..}origin/main
fi
# The range's end: what follows the last `..` (HEAD when nothing does), or the rev itself.
end=$range
case "$range" in *..*) end=${range##*..} ;; esac
end=${end:-HEAD}

commits=$(git log --reverse --format=%H --grep='^AC: ' "$range" --)
if [ -z "$commits" ]; then
  echo "No acceptance criteria in $range."
  exit 0
fi
cites=$("$here/ac-trace.sh" --citations "$end")

# The last value of a trailer of commit $1, as scripts/trailers-lint.sh reads Lander-Slug.
trailer() { git log -1 --format=%B "$1" | git interpret-trailers --parse | sed -n "s/^$2: //p" | tail -n 1; }

# rows: seq<TAB>id<TAB>kind<TAB>sentence<TAB>change, in landing order (the sentence's `|`
# escaped for the table); bad: the change, then the line. A tab is IFS whitespace to `read`,
# so no field may be empty: a change is always the slug or the sha.
rows="" bad="" seq=0
for c in $commits; do
  slug=$(trailer "$c" Lander-Slug)
  short=$(git rev-parse --short "$c")
  change=${slug:-$short}
  while IFS=$'\t' read -r st rest; do
    case "$st" in
      ok) seq=$((seq + 1)); rows="$rows$seq"$'\t'"${rest//|/\\|}"$'\t'"$change"$'\n' ;;
      bad) bad="$bad$short${slug:+ ($slug)}: $rest"$'\n' ;;
    esac
  done < <(git log -1 --format=%B "$c" | "$here/ac-trace.sh" --parse)
done

# One row per id: the kind and sentence of its first landing, every change that declared it.
table=$(printf '%s' "$rows" | CITES=$cites awk -F'\t' '
  BEGIN { n = split(ENVIRON["CITES"], cl, "\n"); for (i = 1; i <= n; i++) { split(cl[i], f, "\t"); if (!(f[1] in at)) at[f[1]] = f[2] } }
  NF >= 5 {
    if (!($2 in kind)) { ids[++m] = $2; kind[$2] = $3; sent[$2] = $4; chg[$2] = $5 }
    else if (index(", " chg[$2] ", ", ", " $5 ", ") == 0) chg[$2] = chg[$2] ", " $5
  }
  END {
    split("manual test deferred", order, " ")
    for (k = 1; k <= 3; k++) for (i = 1; i <= m; i++) {
      id = ids[i]; if (kind[id] != order[k]) continue
      t = "-"; if (kind[id] == "test") t = (id in at) ? at[id] : "not found"
      printf "| %s | %s | %s | %s | %s |\n", id, kind[id], sent[id], chg[id], t
    }
  }')

if [ -z "$table$bad" ]; then
  echo "No acceptance criteria in $range."
  exit 0
fi
if [ -n "$table" ]; then
  echo "Acceptance criteria landed in $range (tests at $end):"
  echo
  echo "| Criterion | Kind | Sentence | Change | Test |"
  echo "|---|---|---|---|---|"
  printf '%s\n' "$table"
else
  echo "No readable acceptance criteria in $range."
fi
# A landed line that does not parse is listed, never dropped: its criterion is in no row above.
if [ -n "$bad" ]; then
  echo
  while IFS= read -r line; do
    [ -z "$line" ] || printf 'unreadable: %s\n' "$line"
  done <<<"$bad"
fi
