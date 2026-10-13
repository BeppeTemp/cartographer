#!/usr/bin/env bash
# Replays demo/astronomy as a git history into <dest-dir> (D745, D751).
#
# Usage: demo/replay.sh <dest-dir>   (dest-dir must not exist yet)
# Needs: bash, git, coreutils only (it runs inside a Docker build stage and on
# macOS's bash 3.2). Shared by demo/build.sh (static Atlas) and
# deploy/demo/Dockerfile (live MCP endpoint).
#
# The KB lands in <dest-dir> as commits spread over the last five months,
# ending yesterday, so Activity and the growth timeline have something to show
# whenever it is built.
set -euo pipefail

dest=${1:?usage: demo/replay.sh <dest-dir>}
repo=$(cd "$(dirname "$0")/.." && pwd)
kb=$dest
mkdir -p "$(dirname "$kb")"
cp -a "$repo/demo/astronomy" "$kb"
cd "$kb"
git init -q -b main
# Each file lands in the first group that names it or a directory above it;
# a file in no group fails the build, so a new KB file is never left out.
groups=(
  "Scaffold the astronomy atlas|instructions.md data/index.md data/log.md data/.gitignore .gitattributes .sops.yaml paths.yaml glossary.yaml"
  "Map the Solar System|data/solar-system"
  "Map the stars|data/stars"
  "Define the glossary|data/glossary"
  "Describe the instruments|data/instruments"
  "Map galaxies and cosmology|data/galaxies"
  "Register the sources|data/sources"
  "Record open questions|data/questions"
  "Add the runbook template|templates"
  "Write the observing runbooks|data/observing"
  "Describe the services and their secrets|services secrets"
  "Ship skills, an agent and a hook|skills agents hooks"
  "Log observing sessions|data/observations"
)
# Each group is split into commits of at most this many files.
chunk=4
commits=()
seen=$'\n' # newline-delimited: macOS's bash 3.2 has no associative arrays
for g in "${groups[@]}"; do
  msg=${g%%|*}
  # shellcheck disable=SC2206
  paths=(${g#*|})
  files=()
  while IFS= read -r f; do
    [[ $seen == *$'\n'"$f"$'\n'* ]] && continue
    seen+="$f"$'\n'
    files+=("$f")
  done < <(git ls-files --others --exclude-standard -- "${paths[@]}" | sort)
  for ((i = 0; i < ${#files[@]}; i += chunk)); do
    commits+=("$msg|${files[*]:i:chunk}")
  done
done
while IFS= read -r f; do
  [[ $seen == *$'\n'"$f"$'\n'* ]] || { echo "replay.sh: $f is in no group" >&2; exit 1; }
done < <(git ls-files --others --exclude-standard)

now=$(date -u +%s)
n=${#commits[@]}
span=$((150 * 86400))
for ((i = 0; i < n; i++)); do
  msg=${commits[i]%%|*}
  # shellcheck disable=SC2206
  files=(${commits[i]#*|})
  # Evenly from 150 days ago to one day ago, at 21:00 UTC give or take.
  at=$((now - span + i * (span - 86400) / (n - 1)))
  at=$((at - at % 86400 + 21 * 3600 + (i * 37 % 120) * 60))
  author="sky-guide"
  [[ $msg == Log* ]] && author="observer"
  git add -- "${files[@]}"
  GIT_AUTHOR_DATE="@$at +0000" GIT_COMMITTER_DATE="@$at +0000" \
    git -c user.name="$author" -c user.email="$author@example.com" commit -q -m "$msg"
done
echo "demo: $n commits replayed into $kb"
