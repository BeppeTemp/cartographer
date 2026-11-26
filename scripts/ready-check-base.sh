#!/usr/bin/env bash
# ready-check-base.sh — the tree the hosted pre-check of a queued change tests
# (ready-check.yml, D1877): its queued chain replayed on the current main. Each job runs it
# right after its checkout, which must hold the full history and the origin/main and
# origin/ready/* refs (`fetch-depth: 0`); it reads them only, it never fetches.
#
# The queued chain of HEAD is HEAD, then the change its `Lander-After: <slug>` trailer names
# while ready/<slug> is queued on origin, and so on: the changes it follows through
# Lander-After, at their queued commits (a predecessor queued again is replayed at its new
# commit). A unit with several tips (b and c both after a) is so tested one path per tip. HEAD must itself be a ready/ tip on origin, or nothing is tested. It is not
# merge-base(origin/main, HEAD)..HEAD: a change queued after a predecessor that landed is a
# child of that predecessor's branch head (scripts/ready.sh), whose commits main already holds
# squashed. The chain is cherry-picked, oldest first, onto origin/main, and the working tree
# is left there (HEAD detached) for the job's next steps; a change queued before a fix landed
# on main is so tested with that fix.
#
# Prints on stdout the union of the paths of each queued commit's own diff, one per line
# (what scripts/ready-check-plan.sh reads); everything else goes to stderr. A replay that
# conflicts exits 1 with `ready-check: the chain does not apply on main: <files>`: a red
# verdict, so the lander gates the unit alone. A plain cherry-pick: a pick the lander
# rescues by an edit of a pending decision main holds numbered is red here too, and costs the
# unit a gate of its own, not a rejection. A conflict in internal/webui/dist only (the committed
# Atlas bundle: two frontend changes always conflict there) is not red: main's side is kept, the
# stderr says `bundle=stale`, and so does `bundle=stale` appended to $GITHUB_OUTPUT when it is set,
# so the jobs test the sources without the bundle's provenance (ready-check.yml); the lander
# builds the bundle again for every commit touching web/ (scripts/land.sh).
# internal/repodocs/landertest/ready_check_test.go pins it. Plain bash 3.2.
set -euo pipefail

# The checkout below can rewrite this very file while bash reads it (and on Windows an open
# file cannot be replaced): it runs from a copy outside the tree.
if [ -z "${READY_CHECK_COPY:-}" ]; then
  copy=$(mktemp "${TMPDIR:-/tmp}/ready-check-base.XXXXXX")
  cp "$0" "$copy"
  READY_CHECK_COPY=$copy exec bash "$copy" "$@"
fi
trap 'rm -f "$READY_CHECK_COPY"' EXIT

main() {
  local ready c a p seen chain=() paths="" files i stale=false
  ready=$(git for-each-ref --format='%(objectname)' 'refs/remotes/origin/ready/')
  git rev-parse -q --verify 'refs/remotes/origin/main^{commit}' >/dev/null ||
    { echo "ready-check: no origin/main in this checkout (fetch-depth: 0)" >&2; exit 1; }
  c=$(git rev-parse HEAD)
  # Fail closed: a checkout without the ready/ refs, or a ref that moved since the push, would
  # test the tip alone and could call a red chain green.
  printf '%s\n' "$ready" | grep -qx "$c" ||
    { echo "ready-check: HEAD $c is not the tip of a ready/ ref on origin (refs missing from the checkout, or the change was queued again): nothing tested" >&2; exit 1; }
  seen=" "
  while :; do
    chain=("$c" "${chain[@]+"${chain[@]}"}")
    paths+=$(git diff --name-only "$c^" "$c")$'\n'
    # As the lander reads it (scripts/land.sh trailer): the first Lander-After trailer.
    a=$(git log -1 --format=%B "$c" | git interpret-trailers --parse | sed -n 's/^Lander-After: *//p' | head -n 1)
    # The lander links by a plain slug match: anything else (`a~1`, `a^{/x}`) links nothing.
    [[ "$a" =~ ^[a-z0-9][a-z0-9-]*$ ]] || break
    case "$seen" in *" $a "*) echo "ready-check: the Lander-After lines loop at $a" >&2; exit 1 ;; esac
    seen+="$a "
    p=$(git rev-parse -q --verify "refs/remotes/origin/ready/$a^{commit}" 2>/dev/null) || break
    printf '%s\n' "$ready" | grep -qx "$p" || break
    # A change that names itself is a unit of one for the lander, not a loop.
    [ "$p" != "$c" ] || break
    c=$p
  done

  git -c advice.detachedHead=false checkout -q --detach refs/remotes/origin/main >&2
  for i in "${!chain[@]}"; do
    c=${chain[i]}
    # -n then commit: a member main already holds replays as an empty commit, not an error.
    if ! git cherry-pick -n "$c" >&2 2>&1; then
      files=$(git diff --name-only --diff-filter=U | tr '\n' ' ')
      if [ -n "$files" ] && ! printf '%s\n' $files | grep -qv '^internal/webui/dist/'; then
        # The whole directory back to main's: a new hashed asset of the change would stay staged
        # beside main's bundle after a plain checkout of HEAD's files.
        git rm -rf -q --cached -- internal/webui/dist >/dev/null 2>&1 || true
        git clean -qfdx -- internal/webui/dist >/dev/null 2>&1 || true
        git checkout HEAD -- internal/webui/dist 2>/dev/null || true
        git add -A internal/webui/dist
        stale=true
      else
        git cherry-pick --abort >/dev/null 2>&1 || git reset -q --hard >/dev/null 2>&1 || true
        echo "ready-check: the chain does not apply on main: ${files% }" >&2
        exit 1
      fi
    fi
    git -c user.name=ready-check -c user.email=ready-check@localhost -c commit.gpgsign=false \
      commit -q --no-verify --allow-empty -C "$c" >&2
  done
  echo "ready-check: ${#chain[@]} queued change(s) replayed on main $(git rev-parse --short refs/remotes/origin/main)" >&2
  if $stale; then
    echo "ready-check: only the Atlas bundle conflicted, main's kept: bundle=stale" >&2
    [ -z "${GITHUB_OUTPUT:-}" ] || echo bundle=stale >>"$GITHUB_OUTPUT"
  fi
  printf '%s' "$paths" | grep . | sort -u || true
}

main "$@"
