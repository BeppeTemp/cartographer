# Sourced by film/publish.sh and film/film-needed.sh: the film's input set (D1068), defined once.
# Everything the render reads: a change to any of these paths can alter or break the film, so their listing at a
# revision is hashed (film_tree) into the `Film-Tree:` trailer of the commit on `readme-film`. A path list, not a
# directory: web/ as a whole changes far more often than the film does. internal/repodocs checks that every
# relative import reachable from film/src/main.ts and film/src/data.ts stays inside this list. bash 3.2 compatible.
FILM_INPUTS=(
    film
    web/src/lib/graph3d
    web/src/lib/palette.ts
    web/src/lib/theme.ts
    web/src/styles/tokens.css
    docs/brand/cartographer.css
    docs/brand/coordinate-pine.svg
    web/package-lock.json
)

# film_tree <rev>: the hash of the film's inputs at <rev>. Returns 2 (naming the path on stderr) when one is missing
# there, so that a renamed input fails loudly instead of hashing to "nothing".
film_tree() {
    local rev="$1" p
    for p in "${FILM_INPUTS[@]}"; do
        git rev-parse --verify --quiet "${rev}:${p}" >/dev/null || { echo "film: input ${p} is missing at ${rev}" >&2; return 2; }
    done
    git ls-tree -r "$rev" -- "${FILM_INPUTS[@]}" | git hash-object --stdin
}
