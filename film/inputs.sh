#!/usr/bin/env bash
# The paths whose content decides the README film (D1068), sourced by publish.sh (which stamps the hash in the
# commit as `Film-Tree:`) and film-needed.sh (which compares it). It is the film's own sources plus everything
# film/src/main.ts imports from outside film/ and the dependency lockfile (three): a change to any of them can
# alter or break the render. internal/repodocs fails when an import of film/src leaves this list.
# bash 3.2 compatible: it runs on macOS too.

FILM_INPUTS=(
    film
    web/src/lib/graph3d
    web/src/lib/palette.ts
    web/src/styles/tokens.css
    docs/brand/cartographer.css
    docs/brand/coordinate-pine.svg
    web/package-lock.json
)

# film_tree <rev>: the object id of the `git ls-tree -r` listing of the paths above at <rev> (one value for
# several paths, unlike `rev-parse <rev>:<dir>`). A path missing at <rev> is exit 2 naming it: a renamed input
# must not silently hash to nothing.
film_tree() {
    local rev="$1" path
    for path in "${FILM_INPUTS[@]}"; do
        git cat-file -e "${rev}:${path}" 2>/dev/null || {
            echo "film: input ${path} is missing at ${rev}" >&2
            return 2
        }
    done
    git ls-tree -r --full-tree "$rev" -- "${FILM_INPUTS[@]}" | git hash-object --stdin
}
