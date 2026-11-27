#!/usr/bin/env bash
# status.sh — what waits for the maintainer, in one command (make status).
#
#   make status [STATUS_PLANS=<command>]
#   make status JSON=1     (status.sh --json) the same state as one JSON object, for any client
#
# Read-only, always exit 0. A section is printed only when it has lines; a read of GitHub that
# fails prints `<section>: unavailable`. In this order:
#   rejected:  each failed/<slug> with its reason (the subject of the rejection commit);
#   queue:     the lander's own view (land.sh --status: the units with their pre-check
#              verdicts, the open train, the gate mode, the lander's state);
#   to read:   the local feat/* and exp/* worktrees not queued whose diff against their merge
#              base touches a production path of a profile no standing approval covers
#              (scripts/profiles.tsv, the rows with a path pattern and standing `no`): the slug
#              and the count of files;
#   main:      the newest ci.yml run on main and the open `ci-red` issues;
#   triage:    the open issues without the `accepted` label, the ideas aside;
#   export:    the open `idea` issues: each is recorded in the backlog, then closed
#              (CONTRIBUTING.md §Issues);
#   trend:     one line, always: the `bug` and `ci-red` issues opened and closed in the last 24
#              hours, by the `Found-by:` value of a body a maintainer or github-actions[bot] wrote
#              (`none` without one);
#   unlanded:  scripts/unlanded.sh: local branches never queued, worktrees with uncommitted changes and
#              the wip/* branches parked on origin;
#   plans:     the output of the command in STATUS_PLANS, when set: plans live outside the
#              repository, so the maintainer points it at their own lister.
# There is no registry: state is the ready/ and failed/ refs and their trailers, the profile is
# profiles.tsv's, the verdict is the pre-check run. It never pushes, writes a ref on origin or
# opens or edits an issue; its fetch (and the one of land.sh --status) updates remote-tracking
# refs, as every fetch does. Plain bash 3.2 (the macOS default).
#
# --json prints one object and nothing else, every key always present (schema 1; a key is added,
# never renamed, and `schema` moves when one changes meaning). `null` is a read that failed, and
# every key but `schema` is null when the object itself cannot be built (no jq):
#   schema       1
#   queue        [{slug, title}]            the ready/* refs on origin
#   rejected     [{slug, reason}]           the failed/* refs, the reason as above
#   issues       {<state>: [{number, class, age_hours, late, blocks_rc, found_by, title}]} | null
#                the rows of scripts/issue-queue.sh --tsv under each of its states (needs-maintainer,
#                queued, rejected, fixing, external, untriaged, open, idle); age_hours is null
#                when GitHub gave no date, found_by a list
#   parked       [<slug>]                   the wip/* branches on origin
#   unlanded     [<line>]                   the lines of scripts/unlanded.sh
#   rc_blockers  [<"#n" | "failed/fix-n">] | null   what makes `release-train` with `action: rc`
#                skip: the issues issue-queue.sh marks, and the failed/fix-* refs
#   lander       {alive, phase, since}      alive: its pid file names a live process; phase and
#                since (an epoch) from its status.json (scripts/land.sh), `stopped` and null when
#                it is not alive and the file does not say when it stopped
# STATUS_NOW=<epoch> is the clock, for a test.
set -uo pipefail
export LC_ALL=C # bytes, never characters: a path with an invalid UTF-8 byte is listed, not an error

here=$(cd "$(dirname "$0")" && pwd)
cd "$(git rev-parse --show-toplevel)" || exit 0
repo=$("$here/upstream-repo.sh" 2>/dev/null) || repo=
now=${STATUS_NOW:-$(date +%s)}
[[ "$now" =~ ^[0-9]+$ ]] || now=$(date +%s)
git fetch -q --no-tags --prune origin main '+refs/heads/ready/*:refs/remotes/origin/ready/*' '+refs/heads/failed/*:refs/remotes/origin/failed/*' \
  '+refs/heads/wip/*:refs/remotes/origin/wip/*' >/dev/null 2>&1 || true

if [ "${1:-}" = --json ]; then
  pairs() { git for-each-ref --format='%(refname:lstrip=4)%09%(contents:subject)' "refs/remotes/origin/$1/" 2>/dev/null; }
  issues=null blockers=null
  if rows=$(ISSUE_QUEUE_NOW=$now "$here/issue-queue.sh" --tsv 2>/dev/null); then
    issues=$(jq -Rn '[inputs | select(. != "") | split("\t") | {state: .[2], number: (.[0] | tonumber), class: .[1],
        age_hours: (.[3] | if . == "-" then null else tonumber end), late: (.[4] == "late"), blocks_rc: (.[5] == "blocks-rc"),
        found_by: (.[6] | if . == "-" then [] else split(",") end), title: (.[7:] | join("\t"))}] as $r |
      reduce ("needs-maintainer", "queued", "rejected", "fixing", "external", "untriaged", "open", "idle") as $s ({};
        .[$s] = [$r[] | select(.state == $s) | del(.state)])' <<<"$rows" 2>/dev/null) || issues=null
    blockers=$({ awk -F'\t' '$6 == "blocks-rc" { print "#" $1 }' <<<"$rows"
      git for-each-ref --format='%(refname:lstrip=3)' 'refs/remotes/origin/failed/fix-*' 2>/dev/null; } | jq -Rn '[inputs | select(. != "")]' 2>/dev/null) || blockers=null
  fi
  # The lander's state directory, as scripts/land.sh names it.
  common=$(git rev-parse --path-format=absolute --git-common-dir)
  lstate=${LAND_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/cartographer-lander/$(printf %s "${common%/.git}" | cksum | cut -d' ' -f1)}
  alive=false
  [ -f "$lstate/pid" ] && kill -0 "$(cat "$lstate/pid" 2>/dev/null)" 2>/dev/null && alive=true
  lander=$(jq -c --argjson alive "$alive" '{alive: $alive, phase: (.phase // null), since: (.since // null)} |
    if $alive or .phase == "stopped" then . else {alive, phase: "stopped", since: null} end' "$lstate/status.json" 2>/dev/null) ||
    lander="{\"alive\":$alive,\"phase\":$($alive && echo null || echo '"stopped"'),\"since\":null}"
  jq -n --argjson issues "${issues:-null}" --argjson blockers "${blockers:-null}" --argjson lander "$lander" \
    --arg queue "$(pairs ready)" --arg rejected "$(pairs failed)" \
    --arg parked "$(git for-each-ref --format='%(refname:lstrip=4)' refs/remotes/origin/wip/ 2>/dev/null)" \
    --arg unlanded "$("$here/unlanded.sh" 2>/dev/null | sed 's/^  //')" '
    def lines: [splits("\n") | select(. != "")];
    def pairs($k): [lines[] | split("\t") | {slug: .[0], ($k): (.[1:] | join("\t"))}];
    {schema: 1, queue: ($queue | pairs("title")), rejected: ($rejected | pairs("reason")), issues: $issues,
     parked: ($parked | lines), unlanded: ($unlanded | lines), rc_blockers: $blockers, lander: $lander}' 2>/dev/null ||
    echo '{"schema":1,"queue":null,"rejected":null,"issues":null,"parked":null,"unlanded":null,"rc_blockers":null,"lander":null}'
  exit 0
fi

# rejected
rej=$(git for-each-ref --format='%(refname:lstrip=4)%09%(contents:subject)' refs/remotes/origin/failed/ 2>/dev/null | sed 's/\t/: /; s/^/  /')
[ -z "$rej" ] || printf 'rejected:\n%s\n' "$rej"

# queue: the lander's status without its own list of rejected changes (printed above)
if q=$("$here/land.sh" --status 2>/dev/null); then
  q=$(awk '/^rejected:/ { skip = 1; next } skip && /^  / { next } { skip = 0; print }' <<<"$q")
  [ -z "$q" ] || printf '%s\n' "$q"
else
  echo "queue: unavailable"
fi

# to read
if [ -r "$here/profiles.tsv" ]; then
  # The path patterns of the rows with standing `no`; a row that is not seven fields is not skipped
  # silently: the section says it is unavailable.
  bad=
  noppat=$(tr -d '\r' <"$here/profiles.tsv" | awk -F'\t' '/^#/ || $1 == "" { next } NF != 7 { print "BADROW " $1; next } $7 == "no" && $3 != "-" { print $3 }')
  if grep -q '^BADROW ' <<<"$noppat"; then bad="a row of profiles.tsv is not seven fields: $(grep -m1 '^BADROW ' <<<"$noppat" | cut -c8-)"; noppat=; fi
  pargs=()
  while read -r pat; do [ -z "$pat" ] || pargs+=(-e "$pat"); done <<<"$noppat"
  toread=
  if [ -n "$noppat" ]; then
    while read -r br; do
      case "$br" in feat/* | exp/*) ;; *) continue ;; esac
      slug=${br#*/}
      git rev-parse -q --verify "refs/remotes/origin/ready/$slug" >/dev/null 2>&1 && continue
      mb=$(git merge-base refs/remotes/origin/main "$br" 2>/dev/null) || continue
      # Production paths only, as the triage counts them (pr-triage.sh --kinds); a failing --kinds
      # is unavailable, not an empty diff.
      if ! kinds=$(git diff --name-only -z "$mb" "$br" 2>/dev/null | "$here/pr-triage.sh" --kinds 2>/dev/null); then
        bad="pr-triage.sh --kinds failed for $slug"; continue
      fi
      prod=$(awk -F'\t' '$1 == "production" { print $2 }' <<<"$kinds")
      [ -n "$prod" ] || continue
      # One grep with every pattern: a path that matches two rows is one file. grep answers 1 for
      # no match and 2 for a pattern it cannot read.
      hits=$(grep -E "${pargs[@]}" <<<"$prod"); rc=$?
      if [ "$rc" = 2 ]; then bad="a path pattern of profiles.tsv is not a regular expression"; continue; fi
      [ "$rc" = 0 ] || continue
      n=$(sort -u <<<"$hits" | wc -l | tr -d ' ')
      toread+="  $slug: $n file(s)"$'\n'
    done < <(git worktree list --porcelain | sed -n 's/^branch refs\/heads\///p')
  fi
  [ -z "$bad" ] || toread="to read: unavailable ($bad)"$'\n'
  if [ -n "$bad" ]; then printf '%s' "$toread"; elif [ -n "$toread" ]; then printf 'to read:\n%s' "$toread"; fi
fi

# main
if [ -n "$repo" ]; then
  run=$(gh run list -R "$repo" --workflow ci.yml --branch main --limit 1 --json status,conclusion \
    --jq 'if length == 0 then "none" else .[0] | (.conclusion // "") as $c | if .status == "completed" then $c else .status end end' 2>/dev/null) && ok=1 || ok=
  red=$(gh issue list -R "$repo" --label ci-red --state open --json number,title --jq '.[] | "  #\(.number) \(.title)"' 2>/dev/null) && ok2=1 || ok2=
  if [ -z "$ok" ] || [ -z "$ok2" ]; then echo "main: unavailable"
  else
    if [ -n "$run" ] || [ -n "$red" ]; then
      echo "main:"
      [ -z "$run" ] || echo "  ci.yml: $run"
      [ -z "$red" ] || sed 's/^  /  ci-red /' <<<"$red"
    fi
  fi
  # triage
  if open=$(gh issue list -R "$repo" --state open --limit 200 --json number,title,labels \
    --jq '.[] | select([.labels[].name] | (index("accepted") or index("idea")) | not) | "  #\(.number) \(.title)"' 2>/dev/null); then
    [ -z "$open" ] || printf 'triage:\n%s\n' "$open"
  else
    echo "triage: unavailable"
  fi
  # export: an idea is not accepted work here, it is recorded in the backlog and closed.
  if ideas=$(gh issue list -R "$repo" --state open --limit 200 --label idea --json number,title --jq '.[] | "  #\(.number) \(.title)"' 2>/dev/null); then
    [ -z "$ideas" ] || printf 'export:\n%s\n' "$ideas"
  else
    echo "export: unavailable"
  fi
  # trend: `since` is GitHub's filter on the last update, so the dates are compared again here.
  cut=$((now - 86400))
  since=$(date -u -d "@$cut" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "$cut" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null) || since=
  if [ -n "$since" ] && moved=$(gh api --paginate "repos/$repo/issues?state=all&since=$since&per_page=100" --jq '
    def found_by: [(.body // "") | split("\n")[] | rtrimstr("\r") | select(test("^Found-by: [a-z-]+$")) | ltrimstr("Found-by: ")];
    def trusted: .user.login == "github-actions[bot]" or (.author_association | IN("OWNER", "MEMBER", "COLLABORATOR"));
    def at($k): (.[$k] // "") | try fromdateiso8601 catch 0;
    .[] | select(.pull_request | not) | select([.labels[].name] | index("bug") or index("ci-red")) |
      ((if trusted then found_by[0] else null end) // "none") as $fb |
      (select(at("created_at") >= '"$cut"') | "opened \($fb)"), (select(at("closed_at") >= '"$cut"') | "closed \($fb)")' 2>/dev/null); then
    part() { awk -v k="$1" '$1 == k { print $2 }' <<<"$moved" | sort | uniq -c |
      awk '{ n += $1; s = s (s == "" ? "" : ", ") $2 " " $1 } END { printf "%d%s", n, (n ? " (" s ")" : "") }'; }
    echo "trend: 24h opened $(part opened), closed $(part closed)"
  else
    echo "trend: unavailable"
  fi
else
  echo "main: unavailable"
  echo "triage: unavailable"
  echo "trend: unavailable"
fi

# unlanded
if u=$("$here/unlanded.sh" 2>/dev/null); then :; fi
[ -z "$u" ] || printf 'unlanded:\n%s\n' "$u"

# plans
if [ -n "${STATUS_PLANS:-}" ]; then
  if p=$(bash -c "$STATUS_PLANS" 2>/dev/null); then
    [ -z "$p" ] || printf 'plans:\n%s\n' "$(sed 's/^/  /' <<<"$p")"
  else
    echo "plans: unavailable"
  fi
fi
exit 0
