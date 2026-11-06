#!/usr/bin/env bash
# affected-pkgs.sh — the Go packages a change can break, for the fast tier
# (`make check`) and the Windows leg of `make signoff`.
#
#   affected-pkgs.sh            changed paths on stdin -> package patterns on stdout
#   affected-pkgs.sh --seeds    the same input -> only the seed directories (no go list)
#   affected-pkgs.sh --code     the same input -> the packages a code change can break,
#                               without the readers of repository files (the Windows
#                               leg: what a reader checks is not platform behaviour)
#   affected-pkgs.sh --changed  the paths this checkout changed since its merge-base with
#                               upstream/main (origin/main without an `upstream` remote),
#                               the working tree and untracked files included
#
# Output: one `./<dir>` per line, `./...` alone when everything is selected, nothing
# when no Go test can see the change. Seeds are the package directories the paths
# touch; the output adds every package whose test binary depends on a code seed (the
# reverse test dependencies inside the module, from `go list -test`). A reader seed
# (a test that reads a repository file outside its package) is not expanded: the file
# changes what that test reads, not the code the importers of its package run.
#
# It is an accelerator, not a proof: `make gate` (and the train gate) still run
# the whole suite, so a reader of repository files this table misses is caught
# there. internal/repodocs/affected_pkgs_test.go pins the table.
#
# Plain bash 3.2 (the macOS default).
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [ "${1:-}" = --changed ]; then
  remote=origin
  git remote get-url upstream >/dev/null 2>&1 && remote=upstream
  base=$(git merge-base "$remote/main" HEAD)
  { git diff --name-only "$base"; git ls-files --others --exclude-standard; } | sort -u
  exit 0
fi

# Every test that reads repository files outside its own package: the repodocs
# checks read docs, scripts, workflows and the Makefile; internal/mcpserver checks
# the tool names the markdown cites; internal/webui hashes web/.
repo_readers="internal/repodocs"
md_readers="internal/repodocs internal/mcpserver"

all=false
seeds="" readers=""
add() { case " $seeds " in *" $1 "*) ;; *) seeds="$seeds $1" ;; esac; }
reader() { case " $readers " in *" $1 "*) ;; *) readers="$readers $1" ;; esac; }

while IFS= read -r p; do
  [ -n "$p" ] || continue
  case "$p" in
    go.mod | go.sum) all=true ;;
    cmd/* | internal/*)
      # The nearest directory that holds Go files: a file in testdata/, an
      # embedded skill or a template belongs to the package that reads it.
      d=$(dirname "$p")
      while :; do
        case "$d" in cmd | internal | .) d=; break ;; esac
        if ls "$d"/*.go >/dev/null 2>&1; then break; fi
        d=$(dirname "$d")
      done
      [ -z "$d" ] || add "$d"
      ;;
    web/*) reader internal/webui ;;
    *.md) for r in $md_readers; do reader "$r"; done ;;
    *) for r in $repo_readers; do reader "$r"; done ;;
  esac
done

if [ "$all" = true ]; then
  echo "./..."
  exit 0
fi
# --code: no readers, and never internal/repodocs, which checks the repository's
# documents wherever it runs (test-shard.sh leaves it out of the Windows CI too).
drop=
[ "${1:-}" != --code ] || { readers=; drop=./internal/repodocs; }

if [ "${1:-}" = --seeds ]; then
  for s in $seeds $readers; do echo "./$s"; done | awk '!seen[$0]++'
  exit 0
fi
[ -n "$seeds" ] || { for r in $readers; do echo "./$r"; done | sort -u; exit 0; }

mod=$(go list -m)
# Every package and every test binary `<pkg>.test` (go list reports one only for a
# package with tests), each with its whole dependency list: a package is selected when
# it is a seed or when it or its test binary depends on one.
go list -test -f '{{if .ForTest}}{{else}}{{.ImportPath}} {{join .Deps " "}}{{end}}' ./... |
  awk -v mod="$mod" -v seeds="$seeds" '
    BEGIN { n = split(seeds, s, " "); for (i = 1; i <= n; i++) want[mod "/" s[i]] = 1 }
    {
      pkg = $1; sub(/\.test$/, "", pkg)
      if (index(pkg, mod "/") != 1) next
      hit = (pkg in want)
      for (i = 2; i <= NF && !hit; i++) if ($i in want) hit = 1
      if (hit) print "./" substr(pkg, length(mod) + 2)
    }
    END { n = split(readers, r, " "); for (i = 1; i <= n; i++) print "./" r[i] }' readers="$readers" |
  { grep -vxF "${drop:-./-none-}" || true; } | sort -u
