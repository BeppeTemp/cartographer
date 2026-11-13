#!/usr/bin/env bash
# Captures the terminal text of the plugins film (film/src/plugins.ts) from the real binary.
#
#   film/transcripts/plugins.sh [out-file]     (default: film/transcripts/plugins.txt)
#
# It runs the steps 1-4 and 7 of docs/plugins.md §Add a script: the example plugin, on the fixture
# test/e2e/fixtures/plugin-example, with bin/cartographer (CARTOGRAPHER_BIN overrides it), in a sandbox whose HOME
# is a temp dir, and writes each command line as `$ <command>` followed by its output verbatim. The sandbox path is
# normalised to $HOME, so the file does not depend on where it was produced. The film imports the file as text; the
# e2e scenario 31_film_transcripts regenerates it and fails on a difference (a change of CLI output fails a test
# instead of leaving a film that lies). `make film-transcripts` rewrites the committed file.
#
# A line starting with `>> ` is a note of the transcript (the film draws it dimmed), not output; a command line starts
# with `$ `. Output is verbatim and may hold any other line, `# ` comments included.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
BIN="${CARTOGRAPHER_BIN:-${ROOT}/bin/cartographer}"
OUT="${1:-${HERE}/plugins.txt}"
[[ -x "$BIN" ]] || { echo "film/transcripts/plugins.sh: binary not found: ${BIN} (make build)" >&2; exit 1; }

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
# A physical path: macOS temp dirs are symlinks, and the binary prints the resolved one.
SANDBOX="$(cd "$SANDBOX" && pwd -P)"
cp -R "${ROOT}/test/e2e/fixtures/plugin-example" "${SANDBOX}/example-forks"
# The store lives in the user cache dir under HOME; nothing else of the environment may leak into the capture.
mkdir -p "${SANDBOX}/.cache"

TMP_OUT="${SANDBOX}/transcript.raw"
: >"$TMP_OUT"

# step <display> <stdin> <args...>: appends `$ <display>`, then the output and, on a non-zero exit, `[exit N]`.
step() {
    local display="$1" input="$2"
    shift 2
    printf '$ %s\n' "$display" >>"$TMP_OUT"
    local rc
    (cd "$SANDBOX" && env -i PATH="$PATH" HOME="$SANDBOX" XDG_CACHE_HOME="${SANDBOX}/.cache" NO_COLOR=1 \
        "$BIN" "$@" <<<"$input") >>"$TMP_OUT" 2>&1
    rc=$?
    [[ $rc -eq 0 ]] || printf '[exit %d]\n' "$rc" >>"$TMP_OUT"
    return 0
}
note() { printf '>> %s\n' "$1" >>"$TMP_OUT"; }

# 1. validate: the registrations.
step 'cartographer plugin validate example-forks' '' plugin validate example-forks
# 2. install, refuse, run: nothing runs before the approval.
step 'cartographer plugin install example-forks' 'n' plugin install example-forks
step 'cartographer example-forks hello' '' example-forks hello
# 3. read what runs, then approve it.
step 'cartographer plugin show example-forks --files' '' plugin show example-forks --files
step 'cartographer plugin approve example-forks --yes' '' plugin approve example-forks --yes
# 4. a setting is not a new version: no second approval.
step 'cartographer plugin config example-forks greeting=hi' '' plugin config example-forks greeting=hi
step 'cartographer example-forks hello ada' '' example-forks hello ada
# 7. one byte of the stored script changed: the plugin stops.
STORED="$(find "$SANDBOX" -path '*plugins/example-forks/*/plugin.star' -type f | head -n 1)"
if [[ -z "$STORED" ]]; then
    echo "film/transcripts/plugins.sh: the stored plugin.star was not found" >&2
    exit 1
fi
chmod u+w "$STORED"
printf '#' >>"$STORED"
note 'one byte appended to the stored plugin.star'
step 'cartographer plugin list' '' plugin list
step 'cartographer example-forks hello ada' '' example-forks hello ada

# Normalise: the sandbox is $HOME, and no absolute path of the machine remains.
sed -e "s|${SANDBOX}|\$HOME|g" "$TMP_OUT" >"$OUT"
if grep -q "$SANDBOX" "$OUT"; then
    echo "film/transcripts/plugins.sh: an absolute path survived the normalisation" >&2
    exit 1
fi
