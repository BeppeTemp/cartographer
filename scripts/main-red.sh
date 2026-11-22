#!/usr/bin/env bash
# main-red.sh <repo> <sha> <last green sha or empty>: what the train gates of a red range of `main`
# did not run, for the `ci-red` issue `main-status` opens or comments on (ci.yml). The job results
# come from NEEDS_JSON, the workflow's toJSON(needs), passed in the environment and never into
# this text. Prints, one per line, what the workflow appends to the issue body:
#
#   Red legs: <names>
#   #<n> train, Gate: <the train PR's Gate line>   or #<n> not a train, <sha12> no pull request
#   The gate of #<n> did not run <leg>: <why>      each red leg a train's gate did not run whole
#   The gate of #<n> is unknown: ...               a train without a readable Gate line
#   Every red leg ran green in the train gate: ... only when every PR of the range is a train
#                                                  whose gate ran every red leg whole
#   Found-by: main-ci
#
# The commits of the range (compare <green>...<sha>, or <sha> alone) are read newest first and
# mapped to their pull request, at most 10 distinct ones: a commit of a pull request already read
# is skipped wherever it is, and from the first commit of an eleventh (or of none) on, the rest of
# the range is counted, not read. A train is a merged PR from this repository's
# own `train/*` branch (a fork can name its branch `train/x` too): the lander opened it, and its
# body's `Gate:` line (scripts/land.sh gate_line, from the signoff status of the gated head) says
# which gate the train had. The line is printed only in the shape gate_line writes: it reaches a
# public issue.
#
# Fail open: a gh or jq failure prints `Gate coverage: could not be read (<step>)` and exits 0,
# so the issue is opened with what there is. Plain bash 3.2 (macOS): no associative arrays.
set -euo pipefail

[ "$#" = 3 ] || { echo "usage: $0 <owner/repo> <sha> <last green sha or empty>" >&2; exit 2; }
repo=$1 sha=$2 green=$3

finish() { echo "Found-by: main-ci"; exit 0; }
unread() { echo "Gate coverage: could not be read ($1)"; finish; }
# A list as `a, b, c`, from one item per line.
commas() { awk 'NF { printf "%s%s", (n++ ? ", " : ""), $0 } END { print "" }'; }

# The red legs: the jobs of `needs` whose result is failure. A job id is [A-Za-z0-9_-]: anything
# else is replaced, so a forged name is inert in the issue body.
[ -n "${NEEDS_JSON:-}" ] || unread needs
legs=$(printf '%s' "$NEEDS_JSON" |
  jq -r 'to_entries[] | select(.value.result == "failure") | .key | gsub("[^A-Za-z0-9_-]"; "_")') || unread needs
if [ -n "$legs" ]; then echo "Red legs: $(printf '%s\n' "$legs" | commas)"; else echo "Red legs: none named"; fi

hex='^[0-9a-f]{7,40}$' repo_re='^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'
[[ "$repo" =~ $repo_re ]] && [[ "$sha" =~ $hex ]] || unread arguments
[ -z "$green" ] || [[ "$green" =~ $hex ]] || unread arguments

commits=""
if [ -n "$green" ]; then
  commits=$(gh api "repos/$repo/compare/$green...$sha" --jq '.commits[].sha' </dev/null) || unread compare
fi
[ -n "$commits" ] || commits=$sha
# Newest first (no tac or tail -r: GNU and BSD each have only one of them).
commits=$(printf '%s\n' "$commits" | awk 'NF { l[++n] = $0 } END { for (i = n; i > 0; i--) print l[i] }')

# The PR that brought the commit to main (the merged one, else the first listed): its number,
# head branch, whether it is merged, and the repository of its head branch.
pull_jq='([.[] | select(.merged_at != null)] + .)[0] // empty
  | [(.number | tostring), .head.ref, (if .merged_at != null then "merged" else "open" end), (.head.repo.full_name // "")]
  | join("\t")'
gate_jq='.body // "" | split("\n") | map(rtrimstr("\r")) | map(select(startswith("Gate: "))) | .[0] // ""'
gate_re='^Gate: (gate=(hosted|host)( [A-Za-z0-9=/._-]+)+|unknown \(the signoff status of [0-9a-f]{12} could not be read\))$'
trains="" others=no seen=" " prs=0 left=0 i=0
total=$(printf '%s\n' "$commits" | awk 'NF { n++ } END { print n + 0 }')
while read -r c; do
  i=$((i + 1))
  [[ "$c" =~ $hex ]] || unread compare
  row=$(gh api "repos/$repo/commits/$c/pulls" --jq "$pull_jq" </dev/null) || unread pulls
  num=""
  if [ -n "$row" ]; then
    IFS=$'\t' read -r num ref merged head <<<"$row"
    [[ "$num" =~ ^[0-9]+$ ]] || unread pulls
    # A train lands as several commits: the cap counts pull requests, not commits.
    case "$seen" in *" $num "*) continue ;; esac
  fi
  # Past the 10th pull request, at most one more call: this commit and the older ones are left.
  if [ "$prs" -ge 10 ]; then left=$((total - i + 1)); break; fi
  if [ -z "$num" ]; then echo "${c:0:12} no pull request"; others=yes; continue; fi
  seen="$seen$num " prs=$((prs + 1))
  case "$merged $ref" in
    "merged train/"*) [ "$head" = "$repo" ] || ref=fork ;;
  esac
  case "$merged $ref" in
    "merged train/"*)
      gate=$(gh api "repos/$repo/pulls/$num" --jq "$gate_jq" </dev/null) || unread body
      if [[ "$gate" =~ $gate_re ]]; then
        echo "#$num train, $gate"
      else
        echo "#$num train, no readable Gate line"
        gate="Gate: unknown"
      fi
      trains="$trains$num ${gate#Gate: }"$'\n' ;;
    *) echo "#$num not a train"; others=yes ;;
  esac
done <<<"$commits"
[ "$left" = 0 ] || echo "and $left more commit(s) of the range, not read"

# cover <leg> <gate>: nothing when that gate ran the leg whole, else why it did not. The table
# is explicit: a leg it does not name is never covered, and TestMainRedKnowsEveryLegOfMainStatus
# fails when main-status watches one it does not name. `host` and `hosted` are compared as whole
# words (one is a prefix of the other), the other words are read by their key.
cover() {
  local mode=${2%% *} w=""
  case "$mode $1" in
    "gate=hosted test" | "gate=hosted test-windows" | "gate=hosted web" | "gate=hosted demo-edge" | \
      "gate=hosted docker" | "gate=hosted helm" | "gate=hosted test-macos" | "gate=hosted test-linux-nosystemd") ;;
    "gate=host test") echo "not its test-install step, the rest ran" ;;
    "gate=host web") case " $2 " in *" web=yes "*) ;; *) echo "no web-gate among its targets" ;; esac ;;
    "gate=host test-windows")
      case " $2 " in *" windows="*) w=${2#*windows=}; w=${w%% *} ;; esac
      case "$w" in
        none) echo "no Windows leg" ;;
        unavailable) echo "the Windows machine was unavailable" ;;
        "" | *[!0-9]*) echo "its Gate line names no Windows leg" ;;
        *) echo "$w package(s) only" ;;
      esac ;;
    "gate=host test-macos" | "gate=host test-linux-nosystemd" | "gate=host docker" | "gate=host helm" | "gate=host demo-edge")
      echo "not a leg of the build-host gate" ;;
    *) echo "a leg scripts/main-red.sh does not know" ;;
  esac
}

# "Every red leg ran green" is a claim about the whole range: a red leg, at least one train,
# every PR of the range a train with a known gate, nothing left unread, and no red leg one of
# those gates did not run whole.
claim=yes
[ -n "$legs" ] && [ -n "$trains" ] && [ "$others" = no ] && [ "$left" = 0 ] || claim=no
while read -r num gate; do
  [ -n "$num" ] || continue
  case "$gate" in
    "gate=hosted "* | "gate=host "*)
      while read -r leg; do
        [ -n "$leg" ] || continue
        why=$(cover "$leg" "$gate")
        [ -n "$why" ] || continue
        echo "The gate of #$num did not run $leg: $why"
        claim=no
      done <<<"$legs" ;;
    *)
      [ -z "$legs" ] || echo "The gate of #$num is unknown: it may not have run $(printf '%s\n' "$legs" | commas)"
      claim=no ;;
  esac
done <<<"$trains"
[ "$claim" = no ] || echo "Every red leg ran green in the train gate: a flaky test or a condition only main has"
finish
