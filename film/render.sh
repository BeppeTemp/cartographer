#!/usr/bin/env bash
# Renders the README films headless (D1068): starts the films' Vite server once, runs film/record.mjs against it for
# each film and stops the server whatever happens. Writes film/out/film.webp for the launch film and
# film/out/<name>.webp for another (and the .mp4 beside it unless --no-mp4).
#
# Usage, from anywhere: film/render.sh [--films a,b] [record.mjs options, e.g. --no-mp4 --frames 30]
# --films picks films of film/films.txt (the one list of films); the default is all of them. The other options go
# to record.mjs as they are. The WebP of each film is capped (film/max-bytes.txt, FILM_MAX_BYTES overrides it; record.mjs).
# Needs `cd web && npm ci` and Chromium (npx playwright install chromium), plus ffmpeg and img2webp (webp package).
# bash 3.2 compatible: it runs on macOS too.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ALL=()
# bash `read` stops a line at a NUL byte, so `launch<NUL>junk` would read as `launch`; record.mjs and film_test.go keep or refuse it,
# so a NUL byte in the list is refused (LC_ALL=C: a BSD tr fails on a byte that is not valid UTF-8).
[ "$(($(wc -c < film/films.txt)))" -eq "$(($(LC_ALL=C tr -d '\000' < film/films.txt | wc -c)))" ] || { echo "render: film/films.txt holds a NUL byte, which is not in a film name" >&2; exit 1; }
# `|| [ -n "$name" ]`: the last line counts without a final newline. A CR (a CRLF file) and the blanks around a name
# are dropped, as record.mjs and vite.config.ts do with trim(). Blank lines are skipped.
while IFS= read -r name || [ -n "$name" ]; do
    name="${name//$'\r'/}"
    name="${name#"${name%%[![:space:]]*}"}"
    name="${name%"${name##*[![:space:]]}"}"
    if [ -n "$name" ]; then ALL+=("$name"); fi
done < film/films.txt
[ "${#ALL[@]}" -gt 0 ] || { echo "render: film/films.txt lists no film" >&2; exit 1; }

PICK=("${ALL[@]}")
REST=()
while [ $# -gt 0 ]; do
    case "$1" in
    --films)
        [ $# -ge 2 ] || { echo "render: --films needs a comma-separated list" >&2; exit 2; }
        IFS=, read -r -a PICK <<<"$2"
        shift 2
        ;;
    *)
        REST+=("$1")
        shift
        ;;
    esac
done
[ "${#PICK[@]}" -gt 0 ] || { echo "render: --films names no film" >&2; exit 2; }
for name in "${PICK[@]}"; do
    known=""
    for a in "${ALL[@]}"; do [ "$a" = "$name" ] && known=1; done
    [ -n "$known" ] || { echo "render: unknown film \"${name}\", film/films.txt lists: ${ALL[*]}" >&2; exit 2; }
done

# The file a film is published under: the launch film keeps the name the README embeds.
webp_of() { if [ "$1" = launch ]; then echo film/out/film.webp; else echo "film/out/$1.webp"; fi; }

mkdir -p film/out
LOG="film/out/vite.log"
URL="http://localhost:5180/"

# Vite's own binary, not `npx vite`: the pid killed below is the server itself, not a wrapper that would orphan it.
(cd web && exec ./node_modules/.bin/vite --config ../film/vite.config.ts) >"$LOG" 2>&1 &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT

up=""
for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null "$URL" 2>/dev/null; then up=1; break; fi
    kill -0 "$SERVER" 2>/dev/null || break
    sleep 1
done
if [ -z "$up" ]; then
    echo "render: the vite server did not answer on ${URL}; last lines of ${LOG}:" >&2
    tail -n 20 "$LOG" >&2
    exit 1
fi

for name in "${PICK[@]}"; do
    out="$(webp_of "$name")"
    rm -f "$out"
    # ${REST[@]+...}: an empty array is an unbound variable under `set -u` in bash 3.2.
    node film/record.mjs --film "$name" ${REST[@]+"${REST[@]}"}
    [ -s "$out" ] || { echo "render: ${out} is missing or empty" >&2; exit 1; }
    echo "render: ${out} is $(wc -c < "$out" | tr -d ' ') bytes"
done
