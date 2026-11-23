#!/usr/bin/env bash
# trailers-lint.sh — the lines of a maintainer's queued change that say where its defect
# was found, what introduced it and what the change cost (CONTRIBUTING.md §Pull requests).
#
#   trailers-lint.sh <message file>   exit 0, or 1 with one reason per line on stderr
#   trailers-lint.sh --values         the values a `Found-by:` line may hold (found-by.tsv)
#
# scripts/ready.sh runs it in the change's worktree, after the title and body rules, so
# origin/main is the main just fetched. The lines start at column 0 anywhere in the
# message: they are not git trailers, because a `Closes #n` line in their paragraph hides
# the whole block from `git interpret-trailers --parse`.
#
# Which trailers a change owes is data (scripts/profiles.tsv): the union of the `trailers`
# column of every row whose title types match the message's title (or that its `Profile:` line
# declares). Today the `fix` row, so a `fix` title (any scope, with or without `!`) needs:
#   - a `Found-by: <value>` line per defect it fixes (a bundle of three has three, the
#     value may repeat), each value a row of scripts/found-by.tsv;
#   - an `Introduced-by: <ref>` line per change that introduced one, each ref one of:
#     `unknown` (alone only); a commit of origin/main (7 to 40 hex digits; a hex word that
#     is no commit is read as a slug); `#<n>`, a pull request, taken as written; the slug
#     of a change landed on origin/main (a commit there carries `Lander-Slug: <slug>`).
#     A `Regression-of: #<n>` line, a contributor's form, counts as one.
# Any change may carry one `Cost: impl <n>k, review <n>k, rounds <n>` line (thousands of
# tokens of the implementing and the reviewing subagents, the review rounds; a figure the
# client does not show is `?`), and nothing else that starts with `Cost:`.
# Each key is matched whatever its case and refused unless spelled as above. A profiles.tsv
# that is missing or malformed, or a row with a path pattern that requires trailers (the path
# is not known here), exits 2 naming the row: the trailers are a safety property.
# Plain bash 3.2 (the macOS default).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
values() { tr -d '\r' <"$here/found-by.tsv" | awk -F'\t' '!/^#/ && $1 != "" { print $1 }'; }
if [ "${1:-}" = "--values" ]; then values; exit 0; fi
msg=${1:-}
[ -n "$msg" ] && [ -f "$msg" ] || { echo "usage: trailers-lint.sh <message file> | --values" >&2; exit 2; }

errs=
refuse() { errs+="trailers-lint: $*"$'\n'; }
trim() { local s=$1; s=${s#"${s%%[![:space:]]*}"}; s=${s%"${s##*[![:space:]]}"}; printf '%s' "$s"; }
lower() { tr '[:upper:]' '[:lower:]' <<<"$1"; }
# The last value of a trailer of commit $1, as scripts/ready.sh reads Lander-Slug.
trailer() { git log -1 --format=%B "$1" | git interpret-trailers --parse | sed -n "s/^$2: //p" | tail -n 1; }

text=$(tr -d '\r' <"$msg")
title=$(head -n 1 <<<"$text")
cost_re='^Cost: impl ([0-9]+k|\?), review ([0-9]+k|\?), rounds ([0-9]+|\?)$'
isfix=0
# The trailers the profiles require of this title: the union over the rows that match it.
ptype=
[[ "$title" =~ ^([a-z]+)(\([a-z,-]+\))?(!?): ]] && ptype=${BASH_REMATCH[1]}
pbreaking=
{ [[ "$title" =~ ^[a-z]+(\([a-z,-]+\))?!: ]] || grep -q '^BREAKING CHANGE' <<<"$text"; } && pbreaking=1
declared=$(sed -n 's/^Profile:[[:space:]]*//p' <<<"$text" | tr -d ' ')
required=
profiles_file=$here/profiles.tsv
[ -r "$profiles_file" ] || { echo "trailers-lint: $profiles_file is missing: the trailers a change owes are read from it" >&2; exit 2; }
lineno=0
while IFS=$'\t' read -r pname ptypes ppaths ptier ptrailers pfreeze pstanding extra || [ -n "$pname" ]; do
  lineno=$((lineno + 1))
  pname=${pname%$'\r'}
  case "$pname" in "" | "#"*) continue ;; esac
  if [ -z "${pstanding:-}" ] || [ -n "${extra:-}" ]; then
    echo "trailers-lint: $profiles_file:$lineno: row '$pname' needs seven tab-separated fields" >&2; exit 2
  fi
  if [ "$ptrailers" != "-" ] && [ "$ppaths" != "-" ]; then
    echo "trailers-lint: $profiles_file:$lineno: row '$pname' has a path pattern and requires trailers: the paths are not known when the message is checked" >&2; exit 2
  fi
  [ "$ptrailers" != "-" ] || continue
  # Only the trailers this script knows how to check: an unknown name would be required of
  # nobody, which is a silent rule.
  for tn in $(tr ',' ' ' <<<"$ptrailers"); do
    case "$tn" in Found-by | Introduced-by) ;; *)
      echo "trailers-lint: $profiles_file:$lineno: row '$pname': unknown trailer '$tn' (Found-by, Introduced-by or -)" >&2; exit 2 ;;
    esac
  done
  hit=
  if [ "$ptypes" = "-" ]; then
    grep -qxF "$pname" <<<"$declared" && hit=1
  elif [ "$ptypes" = "*" ]; then hit=1
  else
    case ",$ptypes," in *",$ptype,"*) [ -z "$ptype" ] || hit=1 ;; esac
    case ",$ptypes," in *,breaking,*) [ -z "$pbreaking" ] || hit=1 ;; esac
  fi
  [ -z "$hit" ] || required+=",$ptrailers,"
done <"$profiles_file"
case "$required" in *,Found-by,* | *,Introduced-by,*) isfix=1 ;; esac
vocab=$(values)

have_main=
need_main() { # origin/main exists, else one reason (once) and false
  if [ -z "$have_main" ]; then
    if git rev-parse -q --verify 'origin/main^{commit}' >/dev/null; then have_main=yes; else
      have_main=no
      refuse "there is no origin/main to look the Introduced-by refs up in: git fetch origin main"
    fi
  fi
  [ "$have_main" = yes ]
}

# check_ref <ref>: refuses a ref that names no change of origin/main.
check_ref() {
  local ref=$1 c
  if [[ "$ref" =~ ^#[0-9]+$ ]]; then return 0; fi
  if [[ "$ref" =~ ^[0-9a-f]{7,40}$ ]] && need_main && c=$(git rev-parse -q --verify "$ref^{commit}"); then
    git merge-base --is-ancestor "$c" origin/main ||
      refuse "Introduced-by: $ref is a commit that is not on origin/main: name the commit or the slug it landed as, or write Introduced-by: unknown"
    return 0
  fi
  if [[ "$ref" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
    need_main || return 0
    # A fixed-string search narrows the log (a slug is not a regex); the trailer decides, whole.
    for c in $(git log origin/main --format=%H --fixed-strings --grep="Lander-Slug: $ref"); do
      [ "$(trailer "$c" Lander-Slug)" = "$ref" ] && return 0
    done
    refuse "Introduced-by: $ref: no commit of origin/main is $ref or carries Lander-Slug: $ref (write Introduced-by: unknown when the change cannot be found)"
    return 0
  fi
  refuse "Introduced-by: '$ref' is none of: unknown, a commit sha, #<n>, a landed change's slug"
}

costs=0 found=0 refs=0 unknowns=0 todo=
while IFS= read -r line; do
  case "$line" in *:*) ;; *) continue ;; esac
  key=${line%%:*} val=$(trim "${line#*:}")
  case "$(lower "$key")" in
    cost)
      costs=$((costs + 1))
      [[ "$line" =~ $cost_re ]] ||
        refuse "a Cost line is exactly 'Cost: impl <n>k, review <n>k, rounds <n>' (an unknown figure is ?): $line" ;;
    found-by)
      [ "$isfix" = 1 ] || continue
      found=$((found + 1))
      if [ "$key" != Found-by ]; then
        refuse "the key is spelled Found-by, not as in: $line"
      elif ! grep -qxF -- "$val" <<<"$vocab"; then
        refuse "Found-by: '$val' is not one of: $(tr '\n' ' ' <<<"$vocab")(scripts/found-by.tsv)"
      fi ;;
    introduced-by | regression-of)
      [ "$isfix" = 1 ] || continue
      refs=$((refs + 1))
      if [ "$(lower "$key")" = introduced-by ] && [ "$key" != Introduced-by ]; then
        refuse "the key is spelled Introduced-by, not as in: $line"
      elif [ -z "$val" ]; then
        refuse "an Introduced-by line names a ref: $line"
      elif [ "$val" = unknown ]; then
        unknowns=$((unknowns + 1))
      else
        todo+="$val"$'\n'
      fi ;;
  esac
done <<<"$text"

[ "$costs" -le 1 ] || refuse "at most one Cost line, the message has $costs"
if [ "$isfix" = 1 ]; then
  [ "$found" -gt 0 ] ||
    refuse "a fix says where its defect was found: a line 'Found-by: <value>' per defect, the value one of: $(tr '\n' ' ' <<<"$vocab")(scripts/found-by.tsv)"
  [ "$refs" -gt 0 ] ||
    refuse "a fix says what introduced its defect: a line 'Introduced-by: <ref>' per change (a sha or slug of origin/main, #<n>), or 'Introduced-by: unknown'"
  [ "$unknowns" = 0 ] || [ "$refs" = 1 ] ||
    refuse "Introduced-by: unknown is allowed alone only: drop it, or the other refs"
  while IFS= read -r ref; do
    [ -z "$ref" ] || check_ref "$ref"
  done <<<"$todo"
fi

[ -z "$errs" ] || { printf '%s' "$errs" >&2; exit 1; }
