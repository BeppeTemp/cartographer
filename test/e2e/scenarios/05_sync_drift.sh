#!/usr/bin/env bash
# scenarios/05_sync_drift.sh — OPERATOR scenario: drift detection.
#
# Verifies the full sync status cycle:
#   (a) After `cartographer connect`, `cartographer status` exits 0 (in-sync).
#   (b) After adding a skill to the KB, `cartographer status` exits 1 (drift).
#   (c) After `cartographer sync --auto-trust`, `cartographer status` exits 0 again.
#
# The oracle is the exit code of `cartographer status` (0=in-sync, 1=drift, 2=error).
# Requires a live server (the client ALWAYS talks over HTTP, see decisions.md).
# Does NOT require the agent.
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"
# shellcheck source=../lib/kb.sh
source "${E2E_DIR}/lib/kb.sh"
# shellcheck source=../lib/server.sh
source "${E2E_DIR}/lib/server.sh"

SCENARIO_NAME="05_sync_drift"

echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

# --- Setup ---
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_make "$KB_DIR"
mkdir -p "$SANDBOX_DIR"

server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT

SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"

# --- (a) initial connect ---
echo "[05a] cartographer connect opencode..."
(cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" connect opencode --server-url "$SERVER_URL") 2>&1 || true

echo ""
echo "--- Assertions (a): in-sync after initial connect ---"
if (cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" status) 2>&1; then
    _assert_pass "status exits 0 (in-sync) after initial connect"
else
    _assert_fail "status exits non-zero (drift/error) when it should be in-sync"
fi

# --- (b) Add skill to the KB to cause drift ---
echo ""
echo "[05b] adding skill to cause drift..."
mkdir -p "${KB_DIR}/skills/my-drift-skill"
cat > "${KB_DIR}/skills/my-drift-skill/SKILL.md" <<'EOF'
---
name: my-drift-skill
description: Test skill for drift detection
version: "1.0"
---
# My Drift Skill
Test.
EOF

echo "--- Assertions (b): drift after adding skill ---"
if (cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" status) 2>&1; then
    _assert_fail "status exits 0 when it should detect drift"
else
    status_code=$?
    if [[ "$status_code" -eq 1 ]]; then
        _assert_pass "status exits 1 (drift) after adding skill"
    else
        _assert_fail "status exits ${status_code} (expected 1=drift)"
    fi
fi

# --- (c) Re-sync to realign (--auto-trust: the new skill comes from the KB, unsigned) ---
echo ""
echo "[05c] cartographer sync --auto-trust to realign..."
(cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" sync --auto-trust) 2>&1 || true

echo "--- Assertions (c): in-sync after re-sync ---"
if (cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" status) 2>&1; then
    _assert_pass "status exits 0 (in-sync) after re-sync"
else
    _assert_fail "status exits non-zero when it should be in-sync after re-sync"
fi

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
