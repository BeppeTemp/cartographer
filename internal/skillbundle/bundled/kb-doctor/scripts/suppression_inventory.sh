#!/bin/sh
# Inventory of every active suppression in a Cartographer KB clone: which
# concept or map silences which check with lint_ignore, and what instructions.md
# accepts with lint_accept. No server tool lists them in one place, and a
# "lint 0" means little without it. READ-ONLY: never writes, never runs git.
# Usage: sh suppression_inventory.sh <kb-clone-root>
# Only the frontmatter is read; a block list is printed in full; an empty list
# silences nothing and is not printed.
set -u
root=${1:?usage: sh suppression_inventory.sh <kb-clone-root>}
cd "$root" 2>/dev/null || { echo "cannot enter $root" >&2; exit 1; }
[ -d data ] || { echo "no data/ under $root" >&2; exit 1; }

# One "<file><TAB><checks>" line per file whose frontmatter has lint_ignore,
# inline ([a, b]) or as a block list (- a).
ignores='
  function flush() { if (on && val != "") print file "\t" val; on = 0; val = "" }
  { sub(/\r$/, "") }
  FNR == 1 { flush(); fm = ($0 == "---"); next }
  !fm { next }
  $0 == "---" { flush(); fm = 0; next }
  on && /^[ \t]*- / { item = $0; sub(/^[ \t]*-[ \t]+/, "", item); val = (val == "" ? item : val ", " item); next }
  on { flush() }
  /^lint_ignore:/ {
    file = FILENAME; sub(/^data\//, "", file)
    v = $0; sub(/^lint_ignore:[ \t]*/, "", v); sub(/[ \t]+$/, "", v)
    sub(/^\[[ \t]*/, "", v); sub(/[ \t]*\]$/, "", v)
    on = 1; val = v
  }
  END { flush() }
'

echo "== concepts with lint_ignore"
find data -name '*.md' ! -name '_map.md' -exec awk "$ignores" {} + | sort
echo "(nothing printed = none)"

echo "== maps with lint_ignore"
find data -name '_map.md' -exec awk "$ignores" {} + | sort
echo "(nothing printed = none)"

echo "== instructions.md lint_accept"
if [ -f instructions.md ]; then
  awk '
    { sub(/\r$/, "") }
    NR == 1 { fm = ($0 == "---"); next }
    !fm || $0 == "---" { exit }
    on && /^[ \t]/ { e = $0; sub(/^[ \t]+/, "", e); if (e !~ /^#/) print e; next }
    /^lint_accept:/ {
      v = $0; sub(/^lint_accept:[ \t]*/, "", v); sub(/[ \t]*#.*$/, "", v)
      if (v != "" && v != "{}") print v
      on = 1; next
    }
    { on = 0 }
  ' instructions.md
fi
echo "(nothing printed = none)"
