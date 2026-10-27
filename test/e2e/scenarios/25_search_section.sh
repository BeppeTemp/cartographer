#!/usr/bin/env bash
# scenarios/25_search_section.sh — OPERATOR scenario: a search hit names the
# `##` section that matched, and flags the pages read whole (D1207).
#
# A real server over HTTP, driven with curl and python3. The KB holds a
# multi-section page, a page of a type its map lists in `read_whole`, and a
# journal entry. Verifies that:
#   A. the multi-section hit carries `section`, the snippet excerpts it, and
#      `concept_read section=<hit.section>` reads it back, smaller than the page;
#   B. the journal entry and the `read_whole` type carry `read: "whole"` and no
#      `section`.
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

SCENARIO_NAME="25_search_section"
echo "=== Scenario ${SCENARIO_NAME} ==="

if ! command -v python3 >/dev/null 2>&1; then
    echo "[ERROR] ${SCENARIO_NAME} needs python3 to read JSON-RPC responses" >&2
    exit 1
fi

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
KB_NAME="kb-a"
KB="${DIR}/${KB_NAME}"
BASE="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
PAD="$(python3 -c 'print("Filler words about nothing in particular. " * 17)')"
mkdir -p "$DIR"
kb_bootstrap "$KB" || exit 1

mkdir -p "${KB}/data/ops" "${KB}/data/incidents"
cat > "${KB}/data/ops/_map.md" <<'EOF2'
---
type: Map
title: Ops
kind: map
read_whole: [Runbook]
---
# Ops
EOF2
cat > "${KB}/data/incidents/_map.md" <<'EOF2'
---
type: Map
title: Incidents
kind: journal
open_statuses: [open]
---
# Incidents
EOF2
page() { # <type> <title> <term>
    printf -- '---\ntype: %s\ntitle: %s\n---\n# %s\n\nIntro.\n\n## Install\n%s\n\n## Rollback\n%s %s\n\n## Contacts\n%s\n\n## Notes\n%s\n' \
        "$1" "$2" "$2" "$PAD" "$3" "$PAD" "$PAD" "$PAD"
}
page Note "Gateway Guide" zxqterm > "${KB}/data/ops/guide.md"
page Runbook "Gateway Runbook" zxqrun > "${KB}/data/ops/runbook.md"
page Incident "Gateway Outage" zxqjournal > "${KB}/data/incidents/e1.md"
git -C "$KB" add -A
git -C "$KB" -c user.name=fixture -c user.email=fixture@example.com commit -q -m "fixture: seed"

server_start "$KB"
server_wait_health 20
trap 'server_stop' EXIT

call() { # <tool> <arguments-json>
    mcp_call "$BASE" "$KB_NAME" "e2e" "$(printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s}}' "$1" "$2")"
}
# hit <search-response> <id> <field> prints one field of a hit ("" if absent).
hit() {
    printf '%s' "$1" | python3 -c '
import json, sys
d = json.load(sys.stdin)
text = "".join(c.get("text", "") for c in d["result"]["content"])
for h in json.loads(text)["results"]:
    if h["id"] == sys.argv[1]:
        print(h.get(sys.argv[2], ""))
' "$2" "$3" 2>/dev/null
}
content_len() { # <tool response> -> length of the text content
    printf '%s' "$1" | python3 -c '
import json, sys
d = json.load(sys.stdin)
print(len("".join(c.get("text", "") for c in d["result"]["content"])))'
}

echo ""
echo "--- A: the multi-section hit ---"
S1="$(call search '{"query":"zxqterm"}')"
assert_mcp_ok "A1 search" "$S1"
SECTION="$(hit "$S1" ops/guide section)"
[[ "$SECTION" == "## Rollback" ]] && _assert_pass "A2 the hit names '## Rollback'" || _assert_fail "A2 section='${SECTION}'"
assert_mcp_contains "A3 the snippet excerpts the section" "$(hit "$S1" ops/guide snippet)" "zxqterm"
READ="$(call concept_read "{\"id\":\"ops/guide\",\"section\":\"${SECTION}\"}")"
assert_mcp_ok "A4 concept_read of the hit's section" "$READ"
assert_mcp_contains "A4 the section reads back with the matched term" "$READ" "zxqterm"
PAGE_BYTES="$(hit "$S1" ops/guide bytes)"
SECTION_LEN="$(content_len "$READ")"
if [[ -n "$PAGE_BYTES" && "$SECTION_LEN" -lt "$PAGE_BYTES" ]]; then
    _assert_pass "A5 the section (${SECTION_LEN} chars) is smaller than the page (${PAGE_BYTES} bytes)"
else
    _assert_fail "A5 section ${SECTION_LEN} vs page ${PAGE_BYTES}"
fi

echo ""
echo "--- B: pages read whole ---"
for pair in "ops/runbook:zxqrun" "incidents/e1:zxqjournal"; do
    id="${pair%%:*}"
    R="$(call search "{\"query\":\"${pair#*:}\"}")"
    [[ "$(hit "$R" "$id" read)" == "whole" ]] && _assert_pass "B ${id}: read whole" || _assert_fail "B ${id}: read='$(hit "$R" "$id" read)'"
    [[ -z "$(hit "$R" "$id" section)" ]] && _assert_pass "B ${id}: no section" || _assert_fail "B ${id}: section='$(hit "$R" "$id" section)'"
done

echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
