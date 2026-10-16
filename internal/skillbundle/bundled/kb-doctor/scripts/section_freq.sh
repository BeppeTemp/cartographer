#!/bin/sh
# Heading frequency for one map: the H2 sections its pages really use, each
# counted once per page. Feeds template enrichment (4b) without reading every
# concept through the API. READ-ONLY: never writes, never runs git.
# Usage: sh section_freq.sh <kb-clone-root> <map> [min-pages=2]
# A page is every .md under data/<map>/ but the map's own _map.md, index.md and
# log.md: an expanded concept's index.md is its page (D310) and counts. Headings
# inside fenced code (``` or ~~~) are skipped, as the section checks skip them.
set -u
usage='usage: sh section_freq.sh <kb-clone-root> <map> [min-pages]'
root=${1:?$usage}
map=${2:?$usage}
min=${3:-2}
cd "$root/data/$map" 2>/dev/null || { echo "no data/$map under $root" >&2; exit 1; }

find . -name '*.md' ! -path './_map.md' ! -path './index.md' ! -path './log.md' -exec awk '
  { sub(/\r$/, "") }
  FNR == 1 { fence = ""; split("", seen) }   # fence state and dedup are per page
  {
    line = $0; sub(/^[ \t]+/, "", line)
    if (fence == "" && (line ~ /^```/ || line ~ /^~~~/)) { fence = substr(line, 1, 1); next }
    if (fence != "") {
      if (substr(line, 1, 1) == fence && line ~ /^(```+|~~~+)[ \t]*$/) fence = ""
      next
    }
  }
  /^## / {
    h = $0; sub(/^##[ \t]+/, "", h); sub(/[ \t#]+$/, "", h)
    if (h != "" && !(h in seen)) { seen[h] = 1; print h }
  }
' {} + | sort | uniq -c |
awk -v m="$min" '{ n = $1; $1 = ""; sub(/^ +/, ""); if (n >= m) printf "%4d  %s\n", n, $0 }' |
sort -rn
