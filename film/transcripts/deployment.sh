#!/usr/bin/env bash
# Captures the terminal text of the deployment film (film/src/deployment.ts) from the real binary and the real files.
#
#   film/transcripts/deployment.sh [out-file]     (default: film/transcripts/deployment.txt)
#
# One block per deployment profile, the rows of the `Deployment scenarios` table in docs/deployment.md that a team picks:
#   row 1  the plan `cartographer setup --dry-run` prints (the binary, a sandbox HOME, nothing changed);
#   row 3  the component lines of the systemd unit and of the compose file of §Example: single VM, taken from the fenced
#          blocks of that page (extracted here, never copied into this script);
#   row 5  the component keys of deploy/helm/cartographer/ci/meridian-full-values.yaml and surveyor-scheduled-values.yaml
#          (the chart's own CI values, copied into the sandbox as they are);
#   row 13 Compass alone, pointed at a remote Meridian: `connect --server-url --dry-run`, the lines that say what would be written.
# The file is the same kind as plugins.txt: a command line is `$ <command>`, output is verbatim, a note is `>> <text>`
# (the film draws it dimmed). The sandbox path is normalised to $HOME. The e2e scenario 26_film_transcripts regenerates
# the file and fails on a difference; `make film-transcripts` rewrites the committed one.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
BIN="${CARTOGRAPHER_BIN:-${ROOT}/bin/cartographer}"
OUT="${1:-${HERE}/deployment.txt}"
[[ -x "$BIN" ]] || { echo "film/transcripts/deployment.sh: binary not found: ${BIN} (make build)" >&2; exit 1; }
die() { echo "film/transcripts/deployment.sh: $*" >&2; exit 1; }

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
# A physical path: macOS temp dirs are symlinks, and the binary prints the resolved one.
SANDBOX="$(cd "$SANDBOX" && pwd -P)"
mkdir -p "${SANDBOX}/.cache"
TMP_OUT="${SANDBOX}/transcript.raw"
: >"$TMP_OUT"

# The inputs of rows 3 and 5, from the tree. The unit and the compose file are the fenced blocks of docs/deployment.md
# whose first line is a comment naming the file (that first line is dropped).
DOC="${ROOT}/docs/deployment.md"
awk '/^```ini$/ { getline l; if (l == "# /etc/systemd/system/cartographer.service") f = 1 } f && /^```$/ { exit } f' "$DOC" >"${SANDBOX}/cartographer.service"
awk '/^```yaml$/ { getline l; if (l == "# compose.yaml") f = 1 } f && /^```$/ { exit } f' "$DOC" >"${SANDBOX}/compose.yaml"
[[ -s "${SANDBOX}/cartographer.service" && -s "${SANDBOX}/compose.yaml" ]] || die "the systemd or compose block of docs/deployment.md §Example: single VM was not found"
cp "${ROOT}/deploy/helm/cartographer/ci/meridian-full-values.yaml" "${ROOT}/deploy/helm/cartographer/ci/surveyor-scheduled-values.yaml" "${SANDBOX}/"

# cmd <display> <command...>: appends `$ <display>`, then the output (stderr included) and, on a non-zero exit, `[exit N]`.
# The command runs in the sandbox with nothing of the environment but a HOME of its own.
cmd() {
    local display="$1" rc
    shift
    printf '$ %s\n' "$display" >>"$TMP_OUT"
    (cd "$SANDBOX" && env -i PATH="$PATH" HOME="$SANDBOX" XDG_CACHE_HOME="${SANDBOX}/.cache" NO_COLOR=1 "$@" </dev/null) >>"$TMP_OUT" 2>&1
    rc=$?
    [[ $rc -eq 0 ]] || printf '[exit %d]\n' "$rc" >>"$TMP_OUT"
    return 0
}
note() { printf '>> %s\n' "$1" >>"$TMP_OUT"; }

note 'row 1 · Laptop: Meridian, Atlas and Compass on one machine'
cmd 'cartographer setup --no-remote --name trial --agents claude --yes --dry-run' \
    "$BIN" setup --no-remote --name trial --agents claude --yes --no-input --dry-run

note 'row 3 · Team VM: Meridian, Atlas and a Surveyor, bearer tokens, TLS at a proxy'
cmd "grep -E '^(User|EnvironmentFile|ExecStart)=' cartographer.service" grep -E '^(User|EnvironmentFile|ExecStart)=' cartographer.service
cmd "grep -E 'image:|command:|env_file:|127.0.0.1' compose.yaml" grep -E 'image:|command:|env_file:|127.0.0.1' compose.yaml

note 'row 5 · Kubernetes: the Helm chart, one Surveyor per perimeter'
cmd "grep -hE '^[a-z]|^  [a-zA-Z]+:|^  - name:|^    (mode|schedule):' meridian-full-values.yaml surveyor-scheduled-values.yaml" grep -hE '^[a-z]|^  [a-zA-Z]+:|^  - name:|^    (mode|schedule):' meridian-full-values.yaml surveyor-scheduled-values.yaml

note 'row 13 · Compass on a workstation, pointed at a remote Meridian'
cmd "cartographer connect claude --server-url https://wiki.example.com --dry-run | grep 'would write'" \
    bash -c "\"$BIN\" connect claude --server-url https://wiki.example.com --dry-run 2>&1 | grep 'would write'"

# Normalise: the sandbox is $HOME, and no absolute path of the machine remains.
sed -e "s|${SANDBOX}|\$HOME|g" "$TMP_OUT" >"$OUT"
if grep -q "$SANDBOX" "$OUT"; then
    die "an absolute path survived the normalisation"
fi
