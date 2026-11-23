#!/usr/bin/env bash
# status.sh — what waits for the maintainer, in one command (make status).
#
#   make status [STATUS_PLANS=<command>]
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
#   triage:    the open issues without the `accepted` label;
#   plans:     the output of the command in STATUS_PLANS, when set: plans live outside the
#              repository, so the maintainer points it at their own lister.
# There is no registry: state is the ready/ and failed/ refs and their trailers, the profile is
# profiles.tsv's, the verdict is the pre-check run. It never pushes, writes a ref on origin or
# opens or edits an issue; its fetch (and the one of land.sh --status) updates remote-tracking
# refs, as every fetch does. Plain bash 3.2 (the macOS default).
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
cd "$(git rev-parse --show-toplevel)" || exit 0
repo=$("$here/upstream-repo.sh" 2>/dev/null) || repo=
git fetch -q --no-tags --prune origin main '+refs/heads/ready/*:refs/remotes/origin/ready/*' '+refs/heads/failed/*:refs/remotes/origin/failed/*' >/dev/null 2>&1 || true

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
      if ! kinds=$(git diff --name-only "$mb" "$br" 2>/dev/null | "$here/pr-triage.sh" --kinds 2>/dev/null); then
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
    --jq '.[] | select([.labels[].name] | index("accepted") | not) | "  #\(.number) \(.title)"' 2>/dev/null); then
    [ -z "$open" ] || printf 'triage:\n%s\n' "$open"
  else
    echo "triage: unavailable"
  fi
else
  echo "main: unavailable"
  echo "triage: unavailable"
fi

# plans
if [ -n "${STATUS_PLANS:-}" ]; then
  if p=$(bash -c "$STATUS_PLANS" 2>/dev/null); then
    [ -z "$p" ] || printf 'plans:\n%s\n' "$(sed 's/^/  /' <<<"$p")"
  else
    echo "plans: unavailable"
  fi
fi
exit 0
