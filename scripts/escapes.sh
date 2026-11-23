#!/usr/bin/env bash
# escapes.sh — the weekly escape report: which phase found the defects fixed on main, which
# review tier let them through, what the changes cost. .github/workflows/escapes.yml posts it
# every Monday as a comment on the open `Escape report` issue.
#
#   escapes.sh [--since <days>] [--no-gh]
#
# Markdown on stdout, seven sections, over the commits of origin/main landed in the last
# <days> days (default 7): 1 the fix commits by `Found-by:` line; 2 where they came from
# (`Introduced-by:`, and the review tier of the change that introduced them beside the
# changes landed with that tier in the 60 days); 3 escapes by phase; 4 the `bug` and `ci-red`
# issues opened in the window by `Found-by:` line, read only from an issue a maintainer or the
# workflows' bot opened (anyone can open one, and would steer the counts otherwise); 5 the eval backlog (eval-case.sh --pending,
# the age of the last line of evals/scores.md); 6 the `Cost:` lines; 7 the open `process`
# issues. The lines are read anywhere in a message, as scripts/trailers-lint.sh reads them, and
# the refs resolve as eval-case.sh resolves them; a bundled fix counts once per line.
#
# It blocks nothing and fails open: with --no-gh, without gh or on any gh failure, sections 4
# and 7 say `not read (<why>)`; a ref that no longer resolves counts as unresolved; a failing
# eval-case.sh is `not read` too. Exit 0; 2 on a usage error, 1 without an origin/main.
# The report goes on a public issue, so it prints shas, counts, issue numbers and the words of
# fixed lists only: a title or a body is never printed, and a `Found-by:` value or a review
# tier outside its list is counted as other.
#
# GH_REPO names the repository gh reads (default: scripts/upstream-repo.sh). ESCAPES_NOW
# (seconds since the epoch) stands in for the clock, for the tests.
# Plain bash 3.2 (the macOS default).
set -euo pipefail
# awk's printf follows LC_NUMERIC: under a comma-decimal locale "%.1f" prints 120,0. The C
# locale also fixes the order sort and grep use.
export LC_ALL=C

usage() { echo "usage: escapes.sh [--since <days>] [--no-gh]" >&2; exit 2; }
days=7 use_gh=1
while [ $# -gt 0 ]; do
  case "$1" in
    --since)
      [[ "${2:-}" =~ ^[1-9][0-9]{0,4}$ ]] || usage
      days=$2; shift 2 ;;
    --no-gh) use_gh=0; shift ;;
    *) usage ;;
  esac
done

here=$(cd "$(dirname "$0")" && pwd)
cd "$(git -C "$here" rev-parse --show-toplevel)"
tip=$(git rev-parse -q --verify 'origin/main^{commit}') || { echo "escapes: there is no origin/main: git fetch origin main" >&2; exit 1; }
now=${ESCAPES_NOW:-$(date +%s)}
[[ "$now" =~ ^[0-9]+$ ]] || { echo "escapes: ESCAPES_NOW is not a number of seconds: $now" >&2; exit 2; }
start=$((now - days * 86400))
start60=$((now - 60 * 86400))
horizon=$start60
if [ "$start" -lt "$horizon" ]; then horizon=$start; fi
vocab=$("$here/trailers-lint.sh" --values)
tab=$'\t' rs=$'\x1e'

# ymd <seconds>: the UTC date, in integer arithmetic (BSD date has no -d); daynum <YYYY-MM-DD>:
# the days since 1970-01-01. The civil-calendar algorithms of H. Hinnant, for dates after 1970.
ymd() {
  awk -v t="$1" 'BEGIN {
    z = int(t / 86400) + 719468; era = int(z / 146097); doe = z - era * 146097
    yoe = int((doe - int(doe / 1460) + int(doe / 36524) - int(doe / 146096)) / 365)
    doy = doe - (365 * yoe + int(yoe / 4) - int(yoe / 100)); mp = int((5 * doy + 2) / 153)
    d = doy - int((153 * mp + 2) / 5) + 1; m = mp < 10 ? mp + 3 : mp - 9
    printf "%04d-%02d-%02d\n", yoe + era * 400 + (m <= 2), m, d }'
}
daynum() {
  awk -v s="$1" 'BEGIN {
    split(s, a, "-"); m = a[2] + 0; d = a[3] + 0; y = a[1] - (m <= 2); era = int(y / 400)
    yoe = y - era * 400; doy = int((153 * (m > 2 ? m - 3 : m + 9) + 2) / 5) + d - 1
    print era * 146097 + yoe * 365 + int(yoe / 4) - int(yoe / 100) + doy - 719468 }'
}
trim() { local s=$1; s=${s#"${s%%[![:space:]]*}"}; s=${s%"${s##*[![:space:]]}"}; printf '%s' "$s"; }
in_vocab() { case $'\n'"$vocab"$'\n' in *$'\n'"$1"$'\n'*) [ -n "$1" ] ;; *) false ;; esac; }

# tier <Review values>: the tier of the last `Review:` value (`<tier>, <n> rounds`), as
# eval-case.sh reads it, one of: light, standard, fast, full, not-recorded (no line), other.
tier() {
  local v=${1##*"$rs"}
  v=$(trim "${v%%,*}")
  case "$v" in
    light | standard | fast | full) printf '%s' "$v" ;;
    "") printf 'not-recorded' ;;
    *) printf 'other' ;;
  esac
}
review_of() { git log -1 --format='%(trailers:key=Review,valueonly,unfold,separator=%x1e)' "$1"; }

# The refs resolve as eval-case.sh resolves them: a sha, the slug of a landed change (its
# `Lander-Slug:` trailer, compared whole) or `#<n>`, the commit whose subject ends in `(#<n>)`.
fix_re='^fix(\([a-z,-]+\))?!?: '
slug_re='^[a-z0-9][a-z0-9-]*$'
# The shape scripts/trailers-lint.sh lets through; any other `Cost:` line counts as none.
cost_re='^Cost: impl ([0-9]+k|\?), review ([0-9]+k|\?), rounds ([0-9]+|\?)$'
subjects=$(git log origin/main --format='%H %s')
by_pr() {
  awk -v want="(#$1)" '{ s = substr($0, 42); w = length(want)
    if (length(s) >= w && substr(s, length(s) - w + 1) == want) { print $1; exit } }' <<<"$subjects"
}
trailer() { git log -1 --format=%B "$1" | git interpret-trailers --parse | sed -n "s/^$2: //p" | tail -n 1; }
# resolve <ref> <fix>: the commit older than the fix that <ref> names, or nothing.
resolve() {
  local ref=$1 fix=$2 c= x
  if [[ "$ref" =~ ^#([0-9]+)$ ]]; then
    c=$(by_pr "${BASH_REMATCH[1]}")
  elif [[ "$ref" =~ ^[0-9a-f]{7,40}$ ]] && x=$(git rev-parse -q --verify "$ref^{commit}"); then
    c=$x # a hex word that is no commit is tried as a slug, below
  elif [[ "$ref" =~ $slug_re ]]; then
    for x in $(git log "$fix^" --format=%H --fixed-strings --grep="Lander-Slug: $ref"); do
      if [ "$(trailer "$x" Lander-Slug)" = "$ref" ]; then c=$x; break; fi
    done
  fi
  if [ -n "$c" ] && git merge-base --is-ancestor "$c" "$fix^"; then echo "$c"; fi
  return 0
}

# One event line per fact, every word from a fixed list or a number; the sections count them.
ev=
while IFS=$'\x1f' read -r sha ct review title; do
  [[ "$sha" =~ ^[0-9a-f]{40}$ && "$ct" =~ ^[0-9]+$ ]] || continue
  if [ "$ct" -lt "$horizon" ] || [ "$ct" -gt "$now" ]; then continue; fi
  t=$(tier "$review")
  if [ "$ct" -ge "$start60" ]; then ev+="landed $t"$'\n'; fi
  if [ "$ct" -lt "$start" ]; then continue; fi
  ev+="change $t"$'\n'
  isfix=0
  if [[ "$title" =~ $fix_re ]]; then isfix=1; fi
  cost= nf=0 refs=
  while IFS=$'\t' read -r key val; do
    case "$key" in
      cost)
        if [ -z "$cost" ] && [[ "$val" =~ $cost_re ]]; then
          cost="${BASH_REMATCH[1]%k} ${BASH_REMATCH[2]%k} ${BASH_REMATCH[3]}"
        fi ;;
      found-by)
        if [ "$isfix" = 1 ]; then
          nf=$((nf + 1))
          if in_vocab "$val"; then ev+="found $val"$'\n'; else ev+="found other"$'\n'; fi
        fi ;;
      introduced-by | regression-of)
        if [ "$isfix" = 1 ] && [ -n "$val" ]; then refs+="$val"$'\n'; fi ;;
    esac
  done < <(git log -1 --format=%B "$sha" | tr -d '\r' | awk '
    { k = tolower($0) }
    k ~ /^(found-by|introduced-by|regression-of):/ {
      i = index($0, ":"); v = substr($0, i + 1); gsub(/^[ \t]+|[ \t]+$/, "", v)
      print substr(k, 1, i - 1) "\t" v; next
    }
    k ~ /^cost:/ { print "cost\t" $0 }')
  if [ -n "$cost" ]; then ev+="cost $t $cost"$'\n'; fi
  if [ "$isfix" = 0 ]; then continue; fi
  ev+="fix"$'\n'
  if [ "$nf" = 0 ]; then ev+="nofound ${sha:0:12}"$'\n'; fi
  if [ -z "$refs" ]; then ev+="noref"$'\n'; continue; fi
  parent=yes
  git rev-parse -q --verify "$sha^" >/dev/null || parent=
  seen=
  while IFS= read -r ref; do
    if [ -z "$ref" ]; then continue; fi
    if [ "$ref" = unknown ]; then ev+="ref unknown"$'\n'; continue; fi
    c=
    if [ -n "$parent" ]; then c=$(resolve "$ref" "$sha"); fi
    if [ -z "$c" ]; then ev+="ref unresolved"$'\n'; continue; fi
    case "$seen" in *"$c"*) continue ;; esac
    seen+="$c "
    ev+="ref resolved $(tier "$(review_of "$c")") $((ct - $(git log -1 --format=%ct "$c")))"$'\n'
  done <<<"$refs"
done < <(git log origin/main --no-merges --format='%H%x1f%ct%x1f%(trailers:key=Review,valueonly,unfold,separator=%x1e)%x1f%s')

# The awk helpers of the sections: a share, a number with at most one decimal, a median.
helpers='
function pct(n, tot) { return tot ? int(100 * n / tot + 0.5) "%" : "0%" }
function num(x,   r) { r = sprintf("%.1f", x); sub(/\.0$/, "", r); return r }
function sortn(a, n,   i, j, v) {
  for (i = 2; i <= n; i++) { v = a[i]; for (j = i - 1; j >= 1 && a[j] > v; j--) a[j + 1] = a[j]; a[j + 1] = v }
}
function median(a, n) { if (!n) return "-"; sortn(a, n); return num(n % 2 ? a[(n + 1) / 2] : (a[n / 2] + a[n / 2 + 1]) / 2) }
function more(n) { return n > 10 ? sprintf(", and %d more", n - 10) : "" }
function tiername(t) { return t == "not-recorded" ? "not recorded" : t }
BEGIN { nt = split("light standard fast full not-recorded", tiers, " ") }'

printf '## Escape report\n\n'
printf '`origin/main` at `%s`, the %d days from %s to %s (UTC). Generated by `scripts/escapes.sh`: counts, shas and issue numbers only.\n\n' \
  "${tip:0:12}" "$days" "$(ymd "$start")" "$(ymd "$now")"

# Sections 1 to 3: the fixes.
printf '%s' "$ev" | VOCAB=$vocab END60=$(ymd "$now") awk "$helpers"'
  BEGIN { nv = split(ENVIRON["VOCAB"], voc, "\n") }
  $1 == "fix" { fixes++ }
  $1 == "found" { found[$2]++; lines++ }
  $1 == "nofound" { nofound++; if (nofound <= 10) nolist = nolist (nolist == "" ? "" : ", ") "`" $2 "`" }
  $1 == "noref" { noref++ }
  $1 == "ref" { ref[$2]++; if ($2 == "resolved") { intro[$3]++; d[++nd] = $4 / 86400 } }
  $1 == "landed" { landed[$2]++ }
  END {
    print "### 1. Fixes\n"
    printf "%d fix commits, %d `Found-by:` lines (a fix that bundles defects has one per defect).\n\n", fixes, lines
    print "| `Found-by` | lines | share |\n|---|---|---|"
    tot = lines + nofound
    for (i = 1; i <= nv; i++) printf "| %s | %d | %s |\n", voc[i], found[voc[i]], pct(found[voc[i]], tot)
    if (found["other"]) printf "| other (not a value of `scripts/found-by.tsv`) | %d | %s |\n", found["other"], pct(found["other"], tot)
    printf "| missing (fixes with no line) | %d | %s |\n\n", nofound, pct(nofound, tot)
    if (nofound) printf "Fixes with no `Found-by:` line: %s%s.\n\n", nolist, more(nofound)

    print "### 2. Where they came from\n"
    print "| `Introduced-by` | lines |\n|---|---|"
    printf "| resolved | %d |\n| unresolved (names no older commit of `main`) | %d |\n", ref["resolved"], ref["unresolved"]
    printf "| unknown | %d |\n| missing (fixes with no line) | %d |\n\n", ref["unknown"], noref
    printf "Median days from the introducing commit to the fix: %s (%d resolved).\n\n", median(d, nd), nd
    printf "| review tier of the introducing change | defects | changes landed in the 60 days to %s |\n|---|---|---|\n", ENVIRON["END60"]
    for (i = 1; i <= nt; i++) printf "| %s | %d | %d |\n", tiername(tiers[i]), intro[tiers[i]], landed[tiers[i]]
    if (intro["other"] || landed["other"]) printf "| other (not a known tier) | %d | %d |\n", intro["other"], landed["other"]
    print ""

    print "### 3. Escapes by phase\n"
    print "A defect found at a phase escaped every phase before it (`scripts/found-by.tsv` is in phase order).\n"
    print "| phase | found there or later | found later (escaped it) |\n|---|---|---|"
    for (i = 1; i <= nv; i++) {
      if (voc[i] == "agent") continue
      at = 0; for (j = i; j <= nv; j++) if (voc[j] != "agent") at += found[voc[j]]
      printf "| %s | %d | %d |\n", voc[i], at, at - found[voc[i]]
    }
    printf "\n`agent` (a session working on something else, outside the order): %d.\n\n", found["agent"]
  }'

# gh reads both issue sections at once; any failure leaves both `not read`.
gh_why= issues= open=
if [ "$use_gh" = 0 ]; then
  gh_why="--no-gh"
elif ! command -v gh >/dev/null 2>&1; then
  gh_why="gh is not installed"
else
  repo=${GH_REPO:-}
  if [ -z "$repo" ]; then repo=$("$here/upstream-repo.sh" 2>/dev/null) || repo=; fi
  if ! [[ "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
    gh_why="no repository to read: set GH_REPO to <owner>/<name>"
  else
    since_iso="$(ymd "$start")T00:00:00Z"
    # number, author association, author type, author login, the value of the first `Found-by:` line.
    jq_issues='.[] | select(.pull_request == null) | select((.created_at | fromdateiso8601) >= '"$start"')
      | [.number, (.author_association // ""), (.user.type // ""), (.user.login // ""),
         ([(.body // "") | splits("\n") | sub("\r$"; "") | select(test("^found-by:"; "i"))] | first // ""
          | sub("^[^:]*:[ \t]*"; "") | sub("[ \t]+$"; ""))] | @tsv'
    gh_issues() {
      gh api -X GET "repos/$repo/issues" -f state=all -f "labels=$1" -f "since=$since_iso" -f per_page=100 \
        --paginate --jq "$jq_issues"
    }
    rc=0
    issues=$(gh_issues bug && gh_issues ci-red) || rc=$?
    if [ "$rc" = 0 ]; then
      open=$(gh api -X GET "repos/$repo/issues" -f state=open -f labels=process -f per_page=100 \
        --paginate --jq '.[] | select(.pull_request == null) | .number') || rc=$?
    fi
    if [ "$rc" != 0 ]; then gh_why="gh failed, exit $rc"; fi
  fi
fi

printf '### 4. Issues opened\n\n'
if [ -n "$gh_why" ]; then
  printf 'not read (%s)\n\n' "$gh_why"
else
  printf '%s\n' "$issues" | sort -t "$tab" -k1,1n -u | VOCAB=$vocab awk -F'\t' "$helpers"'
    BEGIN { nv = split(ENVIRON["VOCAB"], voc, "\n"); for (i = 1; i <= nv; i++) isv[voc[i]] = 1 }
    $1 !~ /^[0-9]+$/ { next }
    {
      maint = ($2 == "OWNER" || $2 == "MEMBER" || $2 == "COLLABORATOR")
      # The line is read from a maintainer or the bot the workflows run as (ci.yml opens the
      # ci-red issues as github-actions[bot], association CONTRIBUTOR); the line of anyone else is ignored.
      fb = (maint || ($3 == "Bot" && $4 == "github-actions[bot]")) ? $5 : ""
      if (fb != "") b = (fb in isv) ? fb : "other"
      else if (maint || $3 == "Bot") b = "missing"
      else b = "user"
      n[b]++; total++
      if (n[b] <= 10) list[b] = list[b] (list[b] == "" ? "" : ", ") "#" $1
    }
    function row(name, b) {
      printf "| %s | %d | %s%s |\n", name, n[b], (list[b] == "" ? "-" : list[b]), more(n[b])
    }
    END {
      printf "%d `bug` or `ci-red` issues opened in the window, by the first `Found-by:` line of their body, read only when their author is a maintainer (`OWNER`, `MEMBER`, `COLLABORATOR`) or the bot the workflows run as (`github-actions[bot]`). ", total
      print "One with no line read counts as `user` when its author is neither a maintainer nor a bot, as missing otherwise.\n"
      print "| `Found-by` | issues | numbers |\n|---|---|---|"
      for (i = 1; i <= nv; i++) row(voc[i], voc[i])
      if (n["other"]) row("other (not a value of `scripts/found-by.tsv`)", "other")
      row("missing", "missing")
      print ""
    }'
fi

printf '### 5. Evals\n\n'
rc=0
pending=$(.claude/skills/cartographer-review-pr/scripts/eval-case.sh --pending) || rc=$?
if [ "$rc" = 0 ]; then
  # One line per (fix, introducing change): a fix that bundles defects of two changes is two rows.
  printf -- '- Pending escapes (`eval-case.sh --pending`): %s rows to add, from %s fixes.\n' \
    "$(printf '%s' "$pending" | awk 'NF { n++ } END { print n + 0 }')" \
    "$(printf '%s\n' "$pending" | awk 'NF && !($1 in f) { f[$1] = 1; n++ } END { print n + 0 }')"
else
  printf -- '- Pending escapes: not read (eval-case.sh failed, exit %s)\n' "$rc"
fi
scores=.claude/skills/cartographer-review-pr/evals/scores.md
last=
if [ -f "$scores" ]; then
  last=$({ grep -E '^\| *[0-9]{4}-[0-9]{2}-[0-9]{2} *\|' "$scores" || true; } | tail -n 1 | sed -E 's/^\| *([0-9-]+).*/\1/')
fi
if [ -z "$last" ]; then
  printf -- '- Last score in `evals/scores.md`: none: run them (`cartographer-review-pr` §Changing the reviewer).\n\n'
else
  age=$(($(daynum "$(ymd "$now")") - $(daynum "$last")))
  if [ "$age" -gt 30 ]; then
    printf -- '- Last score in `evals/scores.md`: %s, %d days ago: run them (`cartographer-review-pr` §Changing the reviewer).\n\n' "$last" "$age"
  else
    printf -- '- Last score in `evals/scores.md`: %s, %d days ago.\n\n' "$last" "$age"
  fi
fi

# Section 6: the cost of every change of the window, by its own review tier.
printf '%s' "$ev" | awk "$helpers"'
  $1 == "change" { changes++; per[$2]++ }
  $1 == "cost" {
    costs++; with[$2]++
    rd = $5 == "?" ? "?" : $5 + 0; r[rd]++
    if ($3 != "?") impl[$2, ++ni[$2]] = $3 + 0
    if ($4 != "?") rev[$2, ++nr[$2]] = $4 + 0
  }
  function tiermedian(src, t, n,   a, i) { for (i = 1; i <= n; i++) a[i] = src[t, i]; return median(a, n) }
  function kilo(m) { return m == "-" ? m : m "k" }
  function costrow(name, t) {
    printf "| %s | %d | %d | %s | %s |\n", name, per[t], with[t], kilo(tiermedian(impl, t, ni[t])), kilo(tiermedian(rev, t, nr[t]))
  }
  END {
    print "### 6. Cost\n"
    printf "%d of %d changes of the window carry a `Cost:` line (`impl` and `review` in thousands of tokens; a `?` figure is left out of the medians).\n\n", costs, changes
    print "| review tier | changes | with `Cost:` | median impl | median review |\n|---|---|---|---|---|"
    for (i = 1; i <= nt; i++) costrow(tiername(tiers[i]), tiers[i])
    if (per["other"]) costrow("other (not a known tier)", "other")
    # Rounds 1 to 3 always (a review has at most three, cartographer-review-pr §5), any other seen.
    print "\n| rounds | changes |\n|---|---|"
    for (v = 1; v <= 3; v++) rounds[++nv] = v
    for (v in r) if (v != "?" && (v + 0 < 1 || v + 0 > 3)) rounds[++nv] = v + 0
    sortn(rounds, nv)
    for (i = 1; i <= nv; i++) printf "| %d | %d |\n", rounds[i], r[rounds[i]]
    printf "| ? | %d |\n\n", r["?"]
  }'

printf '### 7. Process\n\n'
if [ -n "$gh_why" ]; then
  printf 'not read (%s)\n' "$gh_why"
else
  printf 'Open `process` issues: %s.\n' "$(printf '%s' "$open" | awk 'NF { n++ } END { print n + 0 }')"
fi
