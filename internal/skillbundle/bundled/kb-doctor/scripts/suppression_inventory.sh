#!/bin/sh
# Inventory of every active suppression in a Cartographer KB clone: which concept
# or map silences which check. Server tools expose no such list, and a "lint 0"
# is meaningless without it. READ-ONLY: never writes, never runs git.
# Usage: sh suppression_inventory.sh <kb-clone-root>
# A block-form lint_ignore shows its first line only; read the file for the rest.
set -u
root=${1:?usage: sh suppression_inventory.sh <kb-clone-root>}
cd "$root" || exit 1
[ -d data ] || { echo "no data/ under $root" >&2; exit 1; }

echo "== concepts with lint_ignore"
find data -name '*.md' ! -name '_map.md' -exec grep -l '^lint_ignore:' {} + 2>/dev/null |
while read -r f; do
  line=$(grep -m1 '^lint_ignore:' "$f")
  echo "$line" | grep -Eq '^lint_ignore: *\[\] *,?$' && continue   # cleared, silences nothing
  printf '%s\t%s\n' "${f#data/}" "$line"
done
echo "(empty above = none)"

echo "== maps with lint_ignore"
for m in data/*/_map.md; do
  [ -f "$m" ] || continue
  line=$(grep -m1 '^lint_ignore:' "$m" || true)
  [ -n "$line" ] || continue
  echo "$line" | grep -Eq '^lint_ignore: *\[\] *,?$' && continue
  printf '%s\t%s\n' "${m#data/}" "$line"
done
echo "(nothing printed = none)"

echo "== instructions.md lint_accept"
if [ -f instructions.md ]; then
  awk '/^---$/{n++; next} n==1 && /^lint_accept:/{f=1; print; next}
       f && /^[a-zA-Z_]/{f=0} f {print}' instructions.md
fi
echo "(nothing printed = none)"
