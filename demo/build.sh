#!/usr/bin/env bash
# Builds the static Atlas demo (D745) into <out-dir>: the Atlas bundle in
# static mode plus the JSON answers of a real server over demo/astronomy.
#
# Usage: demo/build.sh <out-dir>
# Needs: Go, Node (web/.nvmrc), git, curl. Run from anywhere.
#
# The KB is replayed into a throwaway git repository as a history of commits
# spread over the last five months, ending yesterday, so Activity and the
# growth timeline have something to show whenever the site is built.
set -euo pipefail

out=${1:?usage: demo/build.sh <out-dir>}
repo=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
pid=
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# --- the KB, as a git history -------------------------------------------------
kb="$work/astronomy"
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
  [[ $seen == *$'\n'"$f"$'\n'* ]] || { echo "build.sh: $f is in no group" >&2; exit 1; }
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

# --- the Atlas bundle, static mode ----------------------------------------------
cd "$repo/web"
[ -d node_modules ] || npm ci --silent
VITE_ATLAS_STATIC=1 npx vite build --outDir "$out" --emptyOutDir --logLevel warn

# --- a real server, exported ----------------------------------------------------
cd "$repo"
# The version the top bar shows: the release the site was built from.
version=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || true)
go build -ldflags "-X main.version=${version:-dev}" -o "$work/cartographer" ./cmd/cartographer
port=$((40000 + RANDOM % 20000))
"$work/cartographer" serve --kb "$kb" --http "127.0.0.1:$port" >"$work/serve.log" 2>&1 &
pid=$!
url="http://127.0.0.1:$port"
for _ in $(seq 100); do
  curl -sf "$url/api/ui/v1/kbs" 2>/dev/null | grep -q '"ready":true' && break
  sleep 0.2
done
curl -sf "$url/api/ui/v1/kbs" | grep -q '"ready":true' || { cat "$work/serve.log" >&2; exit 1; }
node "$repo/demo/export.mjs" "$url" astronomy "$out"
echo "demo: static Atlas in $out"
