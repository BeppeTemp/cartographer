#!/bin/sh
# Candidates for harvest (5b): closed journal entries older than N days that are
# not archived yet. Server truth is kb_review kind harvest_candidate; this is the
# zero-token candidate list to build a fan-out mandate from.
# READ-ONLY: never writes, never runs git.
# Usage: sh harvest_queue.sh <kb-clone-root> [days=45]
set -u
root=${1:?usage: sh harvest_queue.sh <kb-clone-root> [days]}
days=${2:-45}
cut=$(date -v-"${days}"d +%Y-%m-%d 2>/dev/null || date -d "${days} days ago" +%Y-%m-%d) || exit 1
cd "$root/data" 2>/dev/null || { echo "no data/ under $root" >&2; exit 1; }

find . -name '*.md' ! -name '_map.md' ! -name 'index.md' ! -name 'archivio-*' ! -name 'archive-*' \
  -exec awk '
    /^---$/ { n++; if (n == 2) exit }
    n < 2 && /^status:/    { s = $2; gsub(/["'\'']/, "", s) }
    n < 2 && /^timestamp:/ { t = $2; gsub(/["'\'']/, "", t) }
    END { if (t != "" && t < "'"${cut}"'")
            if (s == "done" || s == "completed" || s == "resolved" || s == "reference")
              print FILENAME "\t" s "\t" t }
  ' {} + 2>/dev/null | sort -t "$(printf '\t')" -k3
