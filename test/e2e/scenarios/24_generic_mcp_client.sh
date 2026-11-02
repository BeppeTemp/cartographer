#!/usr/bin/env bash
# scenarios/24_generic_mcp_client.sh — OPERATOR scenario: an MCP host with no
# Cartographer client side (D1102).
#
# A real server, driven with curl and python3 only: no `cartographer
# connect/sync`, no bridge, no client config, no skill, no hook, no HOME
# sandbox. It is what Open WebUI, a gateway with an MCP server target or an
# SDK agent loop is, and it asserts what such a host receives. It is the
# acceptance test of the plans that make the server usable alone.
#
# Three connections over the same server (the reader token cannot read
# `work-kb`, the URL of R names it on purpose):
#   R  reader token   /mcp/routed?kbs=kb-a,work-kb
#   W  writer token   /mcp/routed
#   P  writer token   /mcp/routed?provisioned=1   (the control: what an entry
#                                                  written by `connect` looks like)
#
# Verifies, in this order:
#   A. /health with no token names no KB; with a token only the KBs it reads;
#      /mcp/routed refuses a missing or wrong token (401).
#   B. initialize carries the guide: the KBs the token reads, the KB's own
#      instructions, the placeholder bullet, kb_guide; nothing of work-kb.
#   C. tools/list follows the token: read-only for the reader (the same set as
#      the writer on ?access=read), the write tools for the writer, no `kb`
#      property, no work-kb.
#   D. kb_guide returns the guide and a procedure as text, never its frontmatter.
#   E. The first page of a new map is accepted with index_entry, and the
#      commit is authored by the token id (or by Cartographer-Author).
#   F. Refusals do not disclose: a KB the token cannot read looks like one
#      that is not mounted, compared IN THE SAME POSITION (D1096: a ?kbs= name
#      the token cannot read is `unknown`, like an unmounted one, so the name the
#      caller put in its own URL is echoed back; what must hold is that
#      ?kbs=kb-a,work-kb and ?kbs=kb-a,no-such-kb answer alike once the name is
#      substituted); the reader's write is a plain `forbidden`.
#   G. No doctor nudge on a generic connection, and the nudge IS sent to the
#      provisioned control (so "no nudge" is not vacuous).
#   H. No received text sends the agent to a `cartographer <subcommand>`; the
#      listings of the work-kb URL equal those of the no-such-kb URL after the
#      name is substituted, and never carry work-kb's sentinel or page id.
#
# Comparisons (F2, F4, H) mask only the volatile fields: every key ending in
# `_at`, `since` or `no_report_since`, and every key containing `sha` or `hash`.
#
# Traps:
#   - A token with an `id` exists only in the YAML `auth.tokens` form (the flat
#     CARTOGRAPHER_TOKENS cannot carry it): E2E_CONFIG together with E2E_AUTH=true.
#   - The doctor-nudge window is shared by every stateless connection of a KB
#     (one SDK session id, empty): the first successful call of the process
#     claims it. So the order is part of the test: the generic connections call
#     first (D1 is the first tools/call of the run), the provisioned control last.
#   - A KB with no lint debt never nudges, so "no nudge" would pass vacuously:
#     kb-a is seeded with a stray page (dangling link, no inbound link), and F4
#     asserts the debt (conformance.doctor_suggested) before G relies on it.
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

SCENARIO_NAME="24_generic_mcp_client"
echo "=== Scenario ${SCENARIO_NAME} ==="

if ! command -v python3 >/dev/null 2>&1; then
    echo "[ERROR] ${SCENARIO_NAME} needs python3 to read JSON-RPC responses" >&2
    exit 1
fi

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
KB_A="${DIR}/kb-a"
KB_W="${DIR}/work-kb"
CONFIG="${DIR}/config.yaml"
BIN="${REPO_ROOT}/bin/cartographer"
HOST="http://127.0.0.1:${E2E_HTTP_PORT}"
GENERIC_LOG="${DIR}/generic.log"
LISTING_LOG="${DIR}/listing.log"
ADA='Ada Example <ada@example.test>'
TS="$(date +%s)"
WRITER_TOKEN="writer-${TS}"
READER_TOKEN="reader-${TS}"
SENTINEL_A="Sentinel sentence for kb-a ${TS}."
SENTINEL_W="Sentinel sentence for work-kb ${TS}."
W_PAGE="notes/work-only-page-${TS}"
NUDGE="Propose a cartographer-kb-doctor session"
mkdir -p "$DIR"
: > "$GENERIC_LOG"
: > "$LISTING_LOG"

# --- Fixtures: two KBs seeded before the server starts ---
seed() { # <dir> <sentinel>
    kb_bootstrap "$1" || return 1
    printf '\n%s\n' "$2" >> "$1/instructions.md"
}
seed "$KB_A" "$SENTINEL_A"
seed "$KB_W" "$SENTINEL_W"
# The debt of phase G: valid frontmatter, a link to a page that does not exist
# and no inbound link.
mkdir -p "${KB_A}/data/notes"
cat > "${KB_A}/data/notes/stray.md" <<'EOF'
---
type: note
title: Stray note
---
# Stray note

See [[notes/does-not-exist]].
EOF
# A page only work-kb holds: its id must never reach a token that cannot read it.
mkdir -p "${KB_W}/data/notes"
cat > "${KB_W}/data/${W_PAGE}.md" <<'EOF'
---
type: note
title: Work-only page
---
# Work-only page
EOF
for kb in "$KB_A" "$KB_W"; do
    git -C "$kb" add -A
    git -C "$kb" -c user.name=fixture -c user.email=fixture@example.com commit -q -m "fixture: seed"
done

cat > "$CONFIG" <<EOF
http: "127.0.0.1:${E2E_HTTP_PORT}"
auth:
  tokens:
    - token: ${WRITER_TOKEN}
      id: docs-bot
      scopes: [kb:kb-a:rw]
    - token: ${READER_TOKEN}
      id: reader
      scopes: [kb:kb-a:r]
EOF

E2E_AUTH=true E2E_CONFIG="$CONFIG" server_start "${KB_A},${KB_W}"
server_wait_health 20
trap 'server_stop' EXIT

# --- Helpers ---
# rpc <token> <query> <body> [curl args...] prints the raw response body.
rpc() {
    local token="$1" query="$2" body="$3"
    shift 3
    curl -s --max-time 30 -X POST "${HOST}/mcp/routed${query}" \
        -H 'Content-Type: application/json' \
        -H "Authorization: Bearer ${token}" "$@" -d "$body"
}
# rpc_code <token> <query> prints the HTTP status of a tools/list.
rpc_code() {
    curl -s --max-time 30 -o /dev/null -w '%{http_code}' -X POST "${HOST}/mcp/routed${2}" \
        -H 'Content-Type: application/json' ${1:+-H "Authorization: Bearer ${1}"} \
        -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
}
INIT_BODY='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e-generic","version":"1"}}}'
LIST_BODY='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
call_body() { # <tool> <arguments-json>
    printf '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"%s","arguments":%s}}' "$1" "$2"
}
# tcall <token> <query> <tool> <args> [curl args...]
tcall() {
    local token="$1" query="$2" tool="$3" args="$4"
    shift 4
    rpc "$token" "$query" "$(call_body "$tool" "$args")" "$@"
}
# log_generic <file> <body>... appends responses to a transcript.
note() { local f="$1"; shift; printf '%s\n' "$@" >> "$f"; }
# gen <body>: record a response of R or W in generic.log, and echo it.
gen() { note "$GENERIC_LOG" "$1"; printf '%s' "$1"; }
# lst <body>: also record it in listing.log.
lst() { note "$LISTING_LOG" "$1"; gen "$1"; }

# jx <python expr over d> reads a JSON body on stdin and prints the value.
jx() {
    python3 -c '
import json, sys
d = json.load(sys.stdin)
r = d.get("result", {}) if isinstance(d, dict) else {}
exec("out = " + sys.argv[1])
print(out if not isinstance(out, (list, dict)) else json.dumps(out))
' "$1" 2>/dev/null
}
instructions() { jx 'r.get("instructions") or ""'; }
texts() { jx '"\n".join(c.get("text", "") for c in r.get("content", []))'; }
blocks() { jx 'len(r.get("content", []))'; }
tool_names() { jx 'sorted(t["name"] for t in r.get("tools", []))'; }
is_error() { jx '"true" if r.get("isError") else "false"'; }
# canon <name> reads a JSON-RPC response on stdin and prints it canonical: the
# text blocks that are JSON are expanded, the volatile fields masked, and the
# KB name the caller put in its URL replaced by NAME.
canon() {
    python3 -c '
import json, sys
name = sys.argv[1]
def volatile(k):
    return k.endswith("_at") or k in ("since", "no_report_since") or "sha" in k or "hash" in k
def walk(o):
    if isinstance(o, dict):
        return {k: ("<volatile>" if volatile(k) else walk(v)) for k, v in o.items()}
    if isinstance(o, list):
        return [walk(v) for v in o]
    if isinstance(o, str):
        try:
            j = json.loads(o)
            if isinstance(j, (dict, list)):
                return walk(j)
        except ValueError:
            pass
    return o
d = json.load(sys.stdin)
print(json.dumps(walk(d), sort_keys=True, indent=1).replace(name, "NAME"))
' "$1"
}
same_modulo_name() { # <desc> <body-with-work-kb> <body-with-no-such-kb>
    local a b
    a="$(printf '%s' "$2" | canon work-kb)"
    b="$(printf '%s' "$3" | canon no-such-kb)"
    if [[ -n "$a" && "$a" == "$b" ]]; then
        _assert_pass "$1"
    else
        _assert_fail "$1 — work-kb: ${a:0:600} vs no-such-kb: ${b:0:600}"
    fi
}
http_get() { # <token-or-empty> <path>
    curl -s --max-time 30 ${1:+-H "Authorization: Bearer ${1}"} "${HOST}${2}"
}

R_QUERY='?kbs=kb-a,work-kb'
U_QUERY='?kbs=kb-a,no-such-kb'
W_QUERY=''
P_QUERY='?provisioned=1'

echo ""
echo "--- A: liveness surface follows the token ---"

# The first tools/call of the run must be D1: it comes after these requests,
# which are not tools/call.
H_ANON="$(http_get "" /health)"
HC="$(curl -s --max-time 30 -o /dev/null -w '%{http_code}' "${HOST}/health")"
[[ "$HC" == "200" ]] && _assert_pass "A1 /health with no token answers 200" || _assert_fail "A1 /health returned ${HC}"
assert_mcp_contains "A1 /health: status ok" "$H_ANON" '"status":"ok"'
assert_mcp_contains "A1 /health: ready" "$H_ANON" '"ready":true'
assert_mcp_contains "A1 /health: routed path" "$H_ANON" '"routed_path":"/mcp/routed"'
assert_mcp_not_discloses "A1 /health with no token has no kbs" "$H_ANON" '"kbs"'
assert_mcp_not_discloses "A1 /health with no token names no kb-a" "$H_ANON" 'kb-a'
assert_mcp_not_discloses "A1 /health with no token names no work-kb" "$H_ANON" 'work-kb'
for who in "$WRITER_TOKEN" "$READER_TOKEN"; do
    H_TOK="$(http_get "$who" /health)"
    note "$LISTING_LOG" "$H_TOK"
    assert_mcp_contains "A2 /health with a token lists kb-a" "$H_TOK" 'kb-a'
    assert_mcp_not_discloses "A2 /health with a token does not list work-kb" "$H_TOK" 'work-kb'
done
[[ "$(rpc_code "" "")" == "401" ]] && _assert_pass "A3 /mcp/routed with no token: 401" || _assert_fail "A3 no token: $(rpc_code "" "")"
[[ "$(rpc_code "wrong-${TS}" "")" == "401" ]] && _assert_pass "A3 /mcp/routed with a wrong token: 401" || _assert_fail "A3 wrong token: $(rpc_code "wrong-${TS}" "")"

echo ""
echo "--- B: the guide in initialize ---"

INIT_R="$(lst "$(rpc "$READER_TOKEN" "$R_QUERY" "$INIT_BODY")")"
INIT_U="$(lst "$(rpc "$READER_TOKEN" "$U_QUERY" "$INIT_BODY")")"
INIT_W="$(lst "$(rpc "$WRITER_TOKEN" "$W_QUERY" "$INIT_BODY")")"
for pair in "R:$INIT_R" "W:$INIT_W"; do
    who="${pair%%:*}"
    ins="$(printf '%s' "${pair#*:}" | instructions)"
    if [[ -n "$ins" ]]; then _assert_pass "B ${who}: initialize carries instructions"; else _assert_fail "B ${who}: initialize has no instructions"; fi
    for needle in 'kb-a' "$SENTINEL_A" 'search' '{{repo:' 'kb_guide'; do
        assert_mcp_contains "B ${who}: instructions contain '${needle}'" "$ins" "$needle"
    done
    assert_mcp_not_discloses "B ${who}: instructions do not name work-kb" "$ins" 'work-kb'
    assert_mcp_not_discloses "B ${who}: instructions do not carry the work-kb sentinel" "$ins" "$SENTINEL_W"
done
assert_mcp_contains "B W: instructions mention concept_batch" "$(printf '%s' "$INIT_W" | instructions)" 'concept_batch'

echo ""
echo "--- C: the surface follows the token ---"

LIST_R="$(lst "$(rpc "$READER_TOKEN" "$R_QUERY" "$LIST_BODY")")"
LIST_W="$(lst "$(rpc "$WRITER_TOKEN" "$W_QUERY" "$LIST_BODY")")"
LIST_WR="$(rpc "$WRITER_TOKEN" "?access=read" "$LIST_BODY")"
note "$GENERIC_LOG" "$LIST_WR"
NOT_RO="$(printf '%s' "$LIST_R" | jx '[t["name"] for t in r.get("tools", []) if not (t.get("annotations") or {}).get("readOnlyHint")]')"
if [[ "$NOT_RO" == "[]" ]]; then
    _assert_pass "C1 R: every listed tool carries readOnlyHint"
else
    _assert_fail "C1 R lists tools without readOnlyHint: ${NOT_RO}"
fi
NAMES_R="$(printf '%s' "$LIST_R" | tool_names)"
NAMES_W="$(printf '%s' "$LIST_W" | tool_names)"
for t in concept_write concept_batch map_create log_append; do
    assert_mcp_not_discloses "C1 R does not list ${t}" "$NAMES_R" "\"${t}\""
    assert_mcp_contains "C3 W lists ${t}" "$NAMES_W" "\"${t}\""
done
for t in search concept_read kb_guide; do
    assert_mcp_contains "C1 R lists ${t}" "$NAMES_R" "\"${t}\""
done
assert_mcp_contains "C3 W lists kb_guide" "$NAMES_W" '"kb_guide"'
NAMES_WR="$(printf '%s' "$LIST_WR" | tool_names)"
if [[ -n "$NAMES_R" && "$NAMES_R" == "$NAMES_WR" ]]; then
    _assert_pass "C2 R's tool set equals the writer token's on ?access=read"
else
    _assert_fail "C2 R tools differ from the ?access=read view: R=${NAMES_R} read=${NAMES_WR}"
fi
for pair in "R:$LIST_R" "W:$LIST_W"; do
    assert_mcp_not_discloses "C4 ${pair%%:*}: tools/list does not name work-kb" "${pair#*:}" 'work-kb'
    HAS_KB="$(printf '%s' "${pair#*:}" | jx '[t["name"] for t in r.get("tools", []) if "kb" in ((t.get("inputSchema") or {}).get("properties") or {})]')"
    if [[ "$HAS_KB" == "[]" ]]; then
        _assert_pass "C4 ${pair%%:*}: no schema has a kb property"
    else
        _assert_fail "C4 ${pair%%:*}: schemas with a kb property: ${HAS_KB}"
    fi
done

echo ""
echo "--- D: kb_guide ---"

# The first tools/call of the run, by the reader: the one a nudge would hit.
D1="$(lst "$(tcall "$READER_TOKEN" "$R_QUERY" kb_guide '{}')")"
GUIDE_U="$(lst "$(tcall "$READER_TOKEN" "$U_QUERY" kb_guide '{}')")"
assert_mcp_ok "D1 kb_guide with no argument" "$D1"
D1T="$(printf '%s' "$D1" | texts)"
for needle in 'kb-a' "$SENTINEL_A" 'ingest' 'conflict-resolve' 'import'; do
    assert_mcp_contains "D1 kb_guide contains '${needle}'" "$D1T" "$needle"
done
assert_mcp_not_discloses "D1 kb_guide does not name work-kb" "$D1T" 'work-kb'
INGEST_SKILL="${REPO_ROOT}/internal/skillbundle/bundled/cartographer-kb-ingest/SKILL.md"
INGEST_H="$(grep -m1 '^## ' "$INGEST_SKILL")"
D2="$(lst "$(tcall "$READER_TOKEN" "$R_QUERY" kb_guide '{"procedure":"ingest"}')")"
assert_mcp_ok "D2 kb_guide procedure ingest" "$D2"
D2T="$(printf '%s' "$D2" | texts)"
if [[ -n "$INGEST_H" ]]; then
    assert_mcp_contains "D2 the procedure text has the skill's first heading" "$D2T" "$INGEST_H"
else
    _assert_fail "D2 no '## ' heading found in ${INGEST_SKILL}"
fi
assert_mcp_not_discloses "D2 the procedure text has no skill frontmatter" "$D2T" 'name: cartographer-kb-ingest'

echo ""
echo "--- E: the first page, and who wrote it ---"

E1="$(gen "$(tcall "$WRITER_TOKEN" "$W_QUERY" map_create '{"name":"guides","title":"Guides"}')")"
assert_mcp_ok "E1 map_create guides" "$E1"
E2="$(gen "$(tcall "$WRITER_TOKEN" "$W_QUERY" concept_write '{"id":"guides/first","index_entry":true,"frontmatter":{"type":"note","title":"First guide"},"body":"# First guide\n\nThe first page of the map.\n"}')")"
assert_mcp_ok "E2 concept_write of the first page with index_entry" "$E2"
assert_mcp_contains "E2 the response carries curated_indexes" "$E2" 'curated_indexes'
assert_file_exists "${KB_A}/data/guides/first.md"
assert_file_contains "${KB_A}/data/guides/index.md" 'first'
last() { git -C "$KB_A" log -1 --format="$1"; }
[[ "$(last %an)" == "docs-bot" && "$(last %cn)" == "docs-bot" ]] \
    && _assert_pass "E3 the commit is authored and committed by docs-bot" \
    || _assert_fail "E3 author=$(last %an) committer=$(last %cn), want docs-bot"
if last %B | grep -q '^Cartographer-Token: docs-bot$'; then
    _assert_pass "E3 the message carries the Cartographer-Token trailer"
else
    _assert_fail "E3 no Cartographer-Token trailer: $(last %B)"
fi
E4="$(gen "$(tcall "$WRITER_TOKEN" "$W_QUERY" log_append '{"entry":"Checked the first guide."}' -H "Cartographer-Author: ${ADA}")")"
assert_mcp_ok "E4 log_append with Cartographer-Author" "$E4"
[[ "$(last '%an <%ae>')" == "$ADA" && "$(last '%cn <%ce>')" == "$ADA" ]] \
    && _assert_pass "E4 the commit is Ada's, as author and committer" \
    || _assert_fail "E4 author=$(last '%an <%ae>') committer=$(last '%cn <%ce>')"
if last %B | grep -q '^Cartographer-Token: docs-bot$'; then
    _assert_pass "E4 the Cartographer-Token trailer is still docs-bot"
else
    _assert_fail "E4 trailer lost: $(last %B)"
fi
E5="$(gen "$(tcall "$READER_TOKEN" "$R_QUERY" concept_read '{"id":"guides/first"}')")"
assert_mcp_ok "E5 the reader reads what the writer wrote" "$E5"

echo ""
echo "--- F: refusals and non-disclosure ---"

F1="$(gen "$(tcall "$READER_TOKEN" "$R_QUERY" log_append '{"entry":"Not allowed."}')")"
assert_mcp_error_text "F1 the reader's write is refused" "$F1" forbidden
assert_mcp_not_discloses "F1 the refusal does not mention unbound_folder" "$F1" 'unbound_folder'
assert_mcp_not_discloses "F1 the refusal does not mention workspace bind" "$F1" 'workspace bind'
F2A="$(gen "$(tcall "$READER_TOKEN" "$R_QUERY" search '{"query":"sentinel","kb":"work-kb"}')")"
F2B="$(gen "$(tcall "$READER_TOKEN" "$U_QUERY" search '{"query":"sentinel","kb":"no-such-kb"}')")"
if [[ "$(printf '%s' "$F2A" | is_error)" == "true" ]]; then
    _assert_pass "F2 search on a KB the token cannot read is an error"
else
    _assert_fail "F2 search kb=work-kb was not refused: ${F2A:0:300}"
fi
same_modulo_name "F2 a KB the token cannot read answers like an unmounted one, in the same position" "$F2A" "$F2B"
F3A="$(curl -s --max-time 30 -w '\n%{http_code}' -X POST "${HOST}/mcp/routed?kbs=work-kb" -H 'Content-Type: application/json' -H "Authorization: Bearer ${READER_TOKEN}" -d "$LIST_BODY")"
F3B="$(curl -s --max-time 30 -w '\n%{http_code}' -X POST "${HOST}/mcp/routed?kbs=no-such-kb" -H 'Content-Type: application/json' -H "Authorization: Bearer ${READER_TOKEN}" -d "$LIST_BODY")"
note "$GENERIC_LOG" "$F3A" "$F3B"
if [[ "${F3A##*$'\n'}" == "400" && "$(printf '%s' "$F3A" | sed 's/work-kb/NAME/g')" == "$(printf '%s' "$F3B" | sed 's/no-such-kb/NAME/g')" ]]; then
    _assert_pass "F3 ?kbs=work-kb is refused 400, like an unmounted name"
else
    _assert_fail "F3 ?kbs=work-kb: ${F3A} / ?kbs=no-such-kb: ${F3B}"
fi
F4="$(lst "$(tcall "$READER_TOKEN" "$R_QUERY" kb_status '{}')")"
assert_mcp_ok "F4 kb_status" "$F4"
F4T="$(printf '%s' "$F4" | texts)"
F4U="$(lst "$(tcall "$READER_TOKEN" "$U_QUERY" kb_status '{}')")"
same_modulo_name "F4 kb_status on ?kbs=kb-a,work-kb equals the one on ?kbs=kb-a,no-such-kb" "$F4" "$F4U"
DEBT="$(printf '%s' "$F4T" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("unparsable"); raise SystemExit
def find(o):
    if isinstance(o, dict):
        if "doctor_suggested" in o: return o["doctor_suggested"]
        for v in o.values():
            r = find(v)
            if r is not None: return r
    if isinstance(o, list):
        for v in o:
            r = find(v)
            if r is not None: return r
print(str(find(d)).lower())' 2>/dev/null)"
if [[ "$DEBT" == "true" ]]; then
    _assert_pass "F4 conformance.doctor_suggested is true (the debt phase G relies on)"
else
    _assert_fail "F4 conformance.doctor_suggested is '${DEBT}': the seeded debt is gone, so phase G would pass vacuously"
fi

echo ""
echo "--- G: no doctor nudge, with a control ---"

if grep -qF "$NUDGE" "$GENERIC_LOG"; then
    _assert_fail "G1 a generic connection received the doctor nudge: the generic calls consumed the window or the provisioned gate is missing (doctornudge.go)"
else
    _assert_pass "G1 no response of the generic connections carries the doctor nudge"
fi
INIT_P="$(rpc "$WRITER_TOKEN" "$P_QUERY" "$INIT_BODY")"
if [[ -z "$(printf '%s' "$INIT_P" | instructions)" ]]; then
    _assert_pass "G2 a provisioned connection gets no guide in initialize"
else
    _assert_fail "G2 the provisioned connection got instructions"
fi
G2="$(tcall "$WRITER_TOKEN" "$P_QUERY" atlas_overview '{}')"
if [[ "$(printf '%s' "$G2" | blocks)" -ge 2 ]] && printf '%s' "$G2" | texts | grep -qF "$NUDGE"; then
    _assert_pass "G2 the provisioned connection receives the nudge (the debt was real; the gate is the marker)"
else
    _assert_fail "G2 no nudge for the provisioned control: if G1 passed, the likely cause is that the generic calls consumed the shared window or the provisioned gate sits after the claim (doctornudge.go): ${G2:0:300}"
fi
G3="$(curl -s --max-time 30 -w '\n%{http_code}' -X POST "${HOST}/mcp/routed?provisioned=2" -H 'Content-Type: application/json' -H "Authorization: Bearer ${WRITER_TOKEN}" -d "$LIST_BODY")"
if [[ "${G3##*$'\n'}" == "400" && "$G3" == *1* ]]; then
    _assert_pass "G3 ?provisioned=2 is refused 400 and names 1"
else
    _assert_fail "G3 ?provisioned=2: ${G3}"
fi

echo ""
echo "--- H: nothing received sends a generic agent to a CLI ---"

# The word list is the CLI's own subcommand table, read at run time, plus the
# internal hook command.
SUBS="$("$BIN" help 2>&1 | awk '/^  [a-z]/ && $1 != "cartographer" && $1 != "(none)" {print $1}' | sort -u)
hook"
HITS=""
while IFS= read -r sub; do
    [[ -z "$sub" ]] && continue
    if grep -qE "cartographer ${sub}([^A-Za-z0-9_-]|$)" "$GENERIC_LOG"; then
        HITS="${HITS} ${sub}"
    fi
done <<< "$SUBS"
if [[ -z "$HITS" ]]; then
    _assert_pass "H generic.log has no 'cartographer <subcommand>'"
else
    _assert_fail "H generic.log sends the agent to a CLI command:${HITS}"
fi
# The listings of the work-kb URL equal those of the no-such-kb URL.
# (INIT_U and GUIDE_U were taken in B and D, before E created a map: the guide
# lists the archives, so the two sides must be read at the same state.)
LIST_U="$(lst "$(rpc "$READER_TOKEN" "$U_QUERY" "$LIST_BODY")")"
same_modulo_name "H initialize: work-kb URL equals no-such-kb URL" "$INIT_R" "$INIT_U"
same_modulo_name "H tools/list: work-kb URL equals no-such-kb URL" "$LIST_R" "$LIST_U"
same_modulo_name "H kb_guide: work-kb URL equals no-such-kb URL" "$D1" "$GUIDE_U"
for needle in "$SENTINEL_W" "$W_PAGE"; do
    if grep -qF "$needle" "$LISTING_LOG" "$GENERIC_LOG"; then
        _assert_fail "H a response carries work-kb's content: ${needle}"
    else
        _assert_pass "H no response carries work-kb's '${needle}'"
    fi
done

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
