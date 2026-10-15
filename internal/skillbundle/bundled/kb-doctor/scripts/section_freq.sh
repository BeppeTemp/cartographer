#!/bin/sh
# Heading frequency for one map: the sections its pages really use. Feeds template
# enrichment (4b) without reading every concept through the API, and without the
# lint endpoint's finding cap. READ-ONLY: never writes, never runs git.
# Usage: sh section_freq.sh <kb-clone-root> <map> [min-count=2]
# H2 headings outside fenced code blocks; excludes _map.md and index files.
set -u
root=${1:?usage: sh section_freq.sh <kb-clone-root> <map> [min]}
map=${2:?missing map}
min=${3:-2}
cd "$root/data/$map" 2>/dev/null || { echo "no data/$map under $root" >&2; exit 1; }

find . -name '*.md' ! -name '_map.md' ! -name 'index.md' -exec awk '
  /^```/ { code = !code; next }
  !code && /^## / { sub(/^##[ ]*/, ""); print }
' {} + 2>/dev/null | sort | uniq -c |
awk -v m="$min" '{ n = $1; $1 = ""; sub(/^ +/, ""); if (n >= m) printf "%4d  %s\n", n, $0 }' |
sort -rn
