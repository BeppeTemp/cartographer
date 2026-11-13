#!/usr/bin/env bash
# scenarios/29_push_visible.sh — OPERATOR scenario: a push that fails is visible.
#
# A KB whose remote refuses every push: the write commits locally, the push
# fails, and the humans' surfaces say so without an agent asking kb_status:
# /health carries the KB's push state, `cartographer status` prints
# "KB <name>: N commit(s) not pushed, <remedy>" and `cartographer doctor`
# warns. A KB whose push works is mentioned by none of them.
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"
# shellcheck source=../lib/kb.sh
source "${E2E_DIR}/lib/kb.sh"

SCENARIO_NAME="29_push_visible"
echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
mkdir -p "$DIR/sandbox"
# The host has no git identity (run.sh), and a local server refuses a write to a KB with a
# remote without one (D1974): the server file names it, so the write commits and the push is the
# thing that fails.
IDENT_CFG="${DIR}/identity.yaml"
printf 'git:\n  identities:\n    default: "Ada Example <ada@example.test>"\n' > "$IDENT_CFG"
BARE="${DIR}/remote.git"
KB="${DIR}/kbpush"
PORT="${E2E_HTTP_PORT}"
SERVER_URL="http://127.0.0.1:${PORT}/mcp"

PID=""
cleanup() { [[ -n "$PID" ]] && kill "$PID" 2>/dev/null; }
trap cleanup EXIT

git init --bare "$BARE" >/dev/null 2>&1
git -C "$BARE" symbolic-ref HEAD refs/heads/main
kb_bootstrap "$KB"
BRANCH="$(git -C "$KB" branch --show-current)"
git -C "$KB" remote add origin "$BARE" 2>/dev/null
git -C "$KB" push -u origin "$BRANCH" >/dev/null 2>&1

# From now on the remote refuses every push.
mkdir -p "${BARE}/hooks"
printf '#!/bin/sh\necho "remote: push declined by policy" >&2\nexit 1\n' > "${BARE}/hooks/pre-receive"
chmod +x "${BARE}/hooks/pre-receive"

CARTOGRAPHER_AUTH=false K_SERVICE=e2e \
    "$BIN" serve --config "$IDENT_CFG" --kb "$KB" --http "127.0.0.1:${PORT}" >"${DIR}/srv.log" 2>&1 &
PID=$!
elapsed=0
until body=$(curl -sf "${SERVER_URL%/mcp}/health" 2>/dev/null) && grep -q -- '"started_at"' <<<"$body" && ! grep -q -- '"bootstrapping"' <<<"$body"; do
    sleep 1; elapsed=$((elapsed + 1))
    [[ $elapsed -ge 20 ]] && { echo "[ERROR] server not responding"; exit 1; }
done

echo "[29] a write whose push the remote refuses"
curl -sf -X POST "${SERVER_URL}?kb=kbpush" -H 'content-type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"shared","title":"Shared"}}}' >/dev/null

elapsed=0
until grep -q '"push_state":"failed"' <<<"$(curl -sf "${SERVER_URL%/mcp}/health")"; do
    sleep 1; elapsed=$((elapsed + 1))
    [[ $elapsed -ge 30 ]] && break
done

echo ""
echo "--- Assertions ---"
HEALTH="$(curl -sf "${SERVER_URL%/mcp}/health")"
if grep -q -- '"push_state":"failed"' <<<"$HEALTH" && grep -q -- '"unpushed_commits":[1-9]' <<<"$HEALTH"; then
    _assert_pass "/health carries the failed push state and the unpushed commits"
else
    _assert_fail "/health lacks the push state: ${HEALTH}"
fi

SANDBOX="${DIR}/sandbox"
(cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" connect opencode --server-url "$SERVER_URL") >/dev/null 2>&1 || true

STATUS_OUT="$(cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" status 2>&1)"
if grep -Eq -- 'KB kbpush: [0-9]+ commits? not pushed, ' <<<"$STATUS_OUT"; then
    _assert_pass "status prints the KB's unpushed commits with a remedy"
else
    _assert_fail "status does not mention the failed push: ${STATUS_OUT}"
fi

DOCTOR_OUT="$(cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" doctor 2>&1)"
if grep -q -- 'kbpush' <<<"$DOCTOR_OUT" && grep -q -- 'not pushed' <<<"$DOCTOR_OUT"; then
    _assert_pass "doctor warns about the KB whose commits are not pushed"
else
    _assert_fail "doctor does not warn about the failed push: ${DOCTOR_OUT}"
fi

echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
