#!/usr/bin/env bash
# eval-case.sh — an escaped defect becomes a case of evals/cases.md (cartographer-review-pr
# §When a defect escapes), and the ids a run scores (§Changing the reviewer). It spawns nothing.
#
#   eval-case.sh --pending [--since <YYYY-MM-DD>]
#       the escapes of origin/main not yet cases, oldest first, one line each:
#       <fix sha12> <introducing sha12> <tier> <fix title>. --since bounds the fixes read
#       (default: none, all history; the rows already there and the fixes with no resolvable
#       Introduced-by are left out anyway). Exit 0, also when there is none.
#   eval-case.sh <fix sha>
#       appends one row per introducing change of that pending escape at the end of the first
#       table (id c<max+1>; the four cells a person fills are TODO) and prints it. A fix that
#       is not a pending escape is refused with the reasons. It edits nothing else.
#   eval-case.sh --scored
#       the ids a run scores, for eval-setup.sh: the ten newest c rows, every c row of class
#       security, every k row.
#
# An escape is a fix commit of origin/main (title type `fix`, any scope, with or without `!`):
#   - its Found-by lines do not all say `review` (no line at all: a contributor's PR, or a fix
#     queued before the lines existed);
#   - an `Introduced-by:` line (a contributor's `Regression-of:` counts) names a commit older
#     than the fix: a sha, the slug of a landed change (a `Lander-Slug:` trailer, compared
#     whole) or `#<n>`, the commit whose subject ends in `(#<n>)` (a squash-merged PR);
#   - a reviewer read that commit: its `Review:` trailer's tier is `standard` or `full`, or it
#     has none (`not recorded`); `light` had no reviewer;
#   - the pair (fix, introducing commit) is not a row yet. A fix that bundles defects of two
#     changes is two cases.
# The lines are read anywhere in the message, as scripts/trailers-lint.sh reads them (a
# `Closes #n` line beside them hides them from `git interpret-trailers`); the refs take its
# forms. It reads origin/main as fetched (git fetch origin main first) and the cases.md of
# the working copy it runs in, as eval-setup.sh does.
# Plain bash 3.2 (the macOS default).
set -euo pipefail

usage() { echo "usage: eval-case.sh --pending [--since <YYYY-MM-DD>] | --scored | <fix sha>" >&2; exit 2; }
die() { echo "eval-case: $*" >&2; exit 1; }

mode= since= arg=
case "${1:-}" in
  --pending)
    mode=pending; shift
    if [ "${1:-}" = --since ]; then
      [[ "${2:-}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || usage
      since=$2; shift 2
    fi ;;
  --scored) mode=scored; shift ;;
  "" | -*) usage ;;
  *) mode=add arg=$1; shift ;;
esac
[ $# -eq 0 ] || usage

cases="$(git rev-parse --show-toplevel)/.claude/skills/cartographer-review-pr/evals/cases.md"
[ -f "$cases" ] || die "no $cases"

# The cells by position, as eval-setup.sh reads them: $2 id, $4 commit, $5 fixing change, $9 class.
if [ "$mode" = scored ]; then
  awk -F'|' '
    function t(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
    /^\| *c[0-9]+ *\|/ { n = substr(t($2), 2) + 0; c[n] = t($2); if (t($9) == "security") sec[n] = 1; if (n > max) max = n }
    /^\| *k[0-9]+ *\|/ { k = k " " t($2) }
    END {
      for (n = max; n >= 1 && got < 10; n--) if (n in c) { pick[n] = 1; got++ }
      for (n = 1; n <= max; n++) if ((n in pick) || (n in sec)) out = out " " c[n]
      print substr(out k, 2)
    }
  ' "$cases"
  exit 0
fi

git rev-parse -q --verify 'origin/main^{commit}' >/dev/null || die "there is no origin/main: git fetch origin main"

trim() { local s=$1; s=${s#"${s%%[![:space:]]*}"}; s=${s%"${s##*[![:space:]]}"}; printf '%s' "$s"; }
# The last value of a trailer of commit $1, as scripts/trailers-lint.sh reads Lander-Slug.
trailer() { git log -1 --format=%B "$1" | git interpret-trailers --parse | sed -n "s/^$2: //p" | tail -n 1; }
fix_re='^fix(\([a-z,-]+\))?!?: '
pr_re='\(#([0-9]+)\)$'
slug_re='^[a-z0-9][a-z0-9-]*$'
subjects=$(git log origin/main --format='%H %s')
# by_pr <n>: the commit of origin/main pull request #<n> was squash-merged as (newest first).
by_pr() {
  awk -v want="(#$1)" '{ s = substr($0, 42); w = length(want)
    if (length(s) >= w && substr(s, length(s) - w + 1) == want) { print $1; exit } }' <<<"$subjects"
}

# resolve <ref> <fix>: the commit older than the fix that <ref> names, or nothing.
resolve() {
  local ref=$1 fix=$2 c= x
  if [[ "$ref" =~ ^#([0-9]+)$ ]]; then
    c=$(by_pr "${BASH_REMATCH[1]}")
  elif [[ "$ref" =~ ^[0-9a-f]{7,40}$ ]] && x=$(git rev-parse -q --verify "$ref^{commit}"); then
    c=$x # a hex word that is no commit is tried as a slug, below
  elif [[ "$ref" =~ $slug_re ]]; then
    # A fixed-string search narrows the log (a slug is not a regex); the trailer decides, whole.
    for x in $(git log "$fix^" --format=%H --fixed-strings --grep="Lander-Slug: $ref"); do
      if [ "$(trailer "$x" Lander-Slug)" = "$ref" ]; then c=$x; break; fi
    done
  fi
  if [ -n "$c" ] && git merge-base --is-ancestor "$c" "$fix^"; then echo "$c"; fi
  return 0
}

# tier <commit>: the review tier of its Review: trailer (`<tier>, <n> rounds`).
tier() { local v; v=$(trailer "$1" Review); v=$(trim "${v%%,*}"); printf '%s' "${v:-not recorded}"; }

# The rows already there: <introducing commit><TAB><fixing commit> (a fixing cell is a sha12 or
# #<pr>).
known=
while IFS=$'\t' read -r commit fixing; do
  f=
  if [[ "$fixing" =~ ^#([0-9]+)$ ]]; then
    f=$(by_pr "${BASH_REMATCH[1]}")
  elif [[ "$fixing" =~ ^[0-9a-f]{7,40}$ ]]; then
    f=$(git rev-parse -q --verify "$fixing^{commit}" || true)
  fi
  [ -n "$f" ] || continue
  known+="$commit"$'\t'"$f"$'\n'
done < <(awk -F'|' 'function t(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
  /^\| *c[0-9]+ *\|/ { printf "%s\t%s\n", t($4), t($5) }' "$cases")

# pairs <fix>: sets `found` to one <introducing commit><TAB><tier> line per change that makes
# the fix a pending escape, and `why_not` to the reasons for the others.
found= why_not=
pairs() {
  local fix=$1 key val reviews=0 others=0 refs= ref c t
  found= why_not=
  # The lines come only from the message of a commit of origin/main, which a maintainer queued or
  # merged, never from an issue (the fix implementer copies an issue's lines only from a
  # maintainer), and every ref is shape-checked by resolve: trusted as main is.
  while IFS=$'\t' read -r key val; do
    case "$key" in
      found-by) if [ "$val" = review ]; then reviews=$((reviews + 1)); else others=$((others + 1)); fi ;;
      introduced-by | regression-of) if [ -n "$val" ]; then refs+="$val"$'\n'; fi ;;
    esac
  done < <(git log -1 --format=%B "$fix" | tr -d '\r' | awk '
    { k = tolower($0) }
    k ~ /^(found-by|introduced-by|regression-of):/ {
      i = index($0, ":"); v = substr($0, i + 1); gsub(/^[ \t]+|[ \t]+$/, "", v)
      print substr(k, 1, i - 1) "\t" v
    }')
  if [ "$reviews" -gt 0 ] && [ "$others" = 0 ]; then
    why_not="every Found-by line says review: a reviewer found it"; return 0
  fi
  [ -n "$refs" ] || { why_not="no Introduced-by (or Regression-of) line names the change that introduced it"; return 0; }
  git rev-parse -q --verify "$fix^" >/dev/null || { why_not="it has no parent"; return 0; }
  while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    if [ "$ref" = unknown ]; then why_not+="Introduced-by: unknown names no change; "; continue; fi
    c=$(resolve "$ref" "$fix")
    if [ -z "$c" ]; then why_not+="$ref names no commit of origin/main older than the fix; "; continue; fi
    t=$(tier "$c")
    case "$t" in
      standard | full | "not recorded") ;;
      light) why_not+="$ref (${c:0:12}) was reviewed light, by no reviewer; "; continue ;;
      *) why_not+="$ref (${c:0:12}) records the review tier '$t', none of light, standard, full; "; continue ;;
    esac
    if grep -qxF "$c"$'\t'"$fix" <<<"$known"; then why_not+="$ref (${c:0:12}) is already a row; "; continue; fi
    case "$found" in *"$c"*) continue ;; esac
    found+="$c"$'\t'"$t"$'\n'
  done <<<"$refs"
  return 0
}

if [ "$mode" = pending ]; then
  while IFS=$'\t' read -r fix day title; do
    if [ -n "$since" ] && [[ "$day" < "$since" ]]; then continue; fi
    [[ "$title" =~ $fix_re ]] || continue
    pairs "$fix"
    while IFS=$'\t' read -r c t; do
      if [ -n "$c" ]; then printf '%s %s %s %s\n' "${fix:0:12}" "${c:0:12}" "$t" "$title"; fi
    done <<<"$found"
  done < <(git log origin/main --reverse --format='%H%x09%cs%x09%s')
  exit 0
fi

fix=$(git rev-parse -q --verify "$arg^{commit}") || die "$arg is not a commit"
git merge-base --is-ancestor "$fix" origin/main || die "${fix:0:12} is not on origin/main: a fix becomes a case once it has landed"
[[ "$(git log -1 --format=%s "$fix")" =~ $fix_re ]] || die "${fix:0:12} is not a fix (its title's type is not fix)"
pairs "$fix"
[ -n "$found" ] || die "${fix:0:12} is not a pending escape: ${why_not%; }"

# change_of <commit>: how a row names a change: its Lander-Slug, else the pull request its
# subject ends with, else its sha12.
change_of() {
  local s
  s=$(trailer "$1" Lander-Slug)
  if [[ "$s" =~ $slug_re ]]; then printf '%s' "$s"; return 0; fi
  s=$(git log -1 --format=%s "$1")
  if [[ "$s" =~ $pr_re ]]; then printf '#%s' "${BASH_REMATCH[1]}"; return 0; fi
  printf '%s' "${1:0:12}"
}

max=$(awk -F'|' '/^\| *c[0-9]+ *\|/ { n = $2; gsub(/[^0-9]/, "", n); if (n + 0 > m) m = n + 0 } END { print m + 0 }' "$cases")
rows=
while IFS=$'\t' read -r c _; do
  [ -n "$c" ] || continue
  max=$((max + 1))
  rows+="| c$max | $(change_of "$c") | $c | ${fix:0:12} | none | TODO | TODO | TODO | TODO |"$'\n'
done <<<"$found"

# After the last c row, or under the first table's header rule when it has none; every other
# line is printed as it was.
tmp=$(mktemp "${TMPDIR:-/tmp}/eval-case.XXXXXX")
trap 'rm -f "$tmp"' EXIT
ROWS=$rows awk '
  /^\| *c[0-9]+ *\|/ { last = NR }
  !rule && /^\|[-| ]+\|[ \t]*$/ { rule = NR }
  { line[NR] = $0 }
  END {
    at = last ? last : rule
    for (i = 1; i <= NR; i++) { print line[i]; if (i == at) printf "%s", ENVIRON["ROWS"] }
    if (!at) exit 1
  }
' "$cases" >"$tmp" || die "$cases has no table of defects to append to"
cat "$tmp" >"$cases"
printf '%s' "$rows"
