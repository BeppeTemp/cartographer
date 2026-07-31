#!/bin/sh
# Portable installer test orchestrator (D121 / WP3).
#
# Usage:
#   ./test/install/run.sh
#
# Runs every scenario under test/install/scenarios/ against the real
# install.sh with a fake curl and a stateful fake "cartographer" binary — no
# network, no real service. POSIX sh throughout (dash-compatible), mirroring
# install.sh's own portability requirement.

set -eu

INSTALL_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

TOTAL=0
FAILED=0
PASSED=0

for script in "${INSTALL_DIR}"/scenarios/*.sh; do
    [ -f "$script" ] || continue
    TOTAL=$((TOTAL + 1))
    echo ""
    echo "======================================================"
    echo " Scenario: $(basename "$script" .sh)"
    echo "======================================================"
    if sh "$script"; then
        PASSED=$((PASSED + 1))
    else
        FAILED=$((FAILED + 1))
    fi
done

echo ""
echo "======================================================"
echo " Install Test Report"
echo "======================================================"
echo " Total   : ${TOTAL}"
echo " Passed  : ${PASSED}"
echo " Failed  : ${FAILED}"
echo "======================================================"

if [ "$FAILED" -gt 0 ]; then
    echo "[run] FAIL — ${FAILED} scenario(s) failed"
    exit 1
fi

echo "[run] PASS — all scenarios passed"
