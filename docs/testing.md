# Testing — Strategies and levels

## Core principle

A real agent interacts with Cartographer **exclusively** through:
- MCP tools (`POST /mcp?kb=<name>`, list in `control-plane.md` §API MCP)
- Bundled skills (e.g. `kb-create`)
- LLM client hooks

It has no access to the server's filesystem, cannot restart containers, cannot run curl directly. Any test that uses these privileged channels verifies the server but **does not verify agents' ability to operate autonomously**.

The core distinction is:

| Level | Who runs it | Tools |
|---|---|---|
| **Operator** | Human / CI | filesystem, `make`, `cartographer serve`/`service`/`connect`/`status`/`sync` |
| **Agent** | LLM agent | Only MCP tools + skills + hooks |

Agent-level integration tests must be written at the agent level: an agent that receives a mandate and uses only MCP to complete it.

---

## Test levels

### 1. Go unit tests
```
make test     # go test ./... — all packages
make vet      # go vet ./...
```
Cover the internal logic: OKF, KB, search, auth, configurator, skill loader, etc.
Fast, do not require a live HTTP server.

### 2. Stdio smoke test
```
make smoke
```
Verifies that the binary responds correctly to the MCP handshake via stdio.

### 3. HTTP smoke test (operator-level)
```
make smoke-http    # scripts/test-kb-flow.sh
```
Verifies the end-to-end HTTP flow with direct curl: starts the native binary on a temporary data dir (port 18081, `CARTOGRAPHER_AUTH=false`), creates a KB, a map and an expanded concept, and does a full cleanup (process + directory). Self-contained: touches neither the real data dir nor requires external services. Useful for CI but **not** an agent-level test: it bypasses the agent's cognitive layer.

### 4. Manual agent-level test
An agent receives a natural-language mandate ("create two maps, one concept each,
expand one and add a satellite, then verify with `atlas_overview`") and must complete it using **only the
MCP tools** — with the server configured via `cartographer connect`. Operator prerequisite: the
KB must already exist and be mounted (an agent cannot create a KB from scratch, by design).
The automated, repeatable version of this test is level 5 below; the manual one remains
useful for exploring new mandates before locking them into a scenario.

---

## What is NOT testable by the agent

| Operation | Why it's operator-level |
|---|---|
| Creating a new KB | Requires `mkdir` on the server's filesystem + restarting the server process |
| Modifying the server config | CLI flag / env var, not exposable via MCP |
| Reading the server logs | Logs on the process's stderr, not via MCP |

These operations are intentionally out of scope for agents (principle of least privilege).

---

### 5. Agent-level E2E with OpenCode

```
E2E_LLM_BASE_URL=https://api.example.com/v1 make e2e   # all scenarios
E2E_LLM_BASE_URL=... make e2e-quick                    # only 01_mcp_crud (minimum clarity gate)
```

Harness that drives **OpenCode in headless mode** as a real agent against a local server.
The AGENT scenarios require `E2E_LLM_BASE_URL` (OpenAI-compatible LLM endpoint):
without it they fail immediately with a clear message, while the OPERATOR scenarios run regardless.
The default model is `opencode-go/deepseek-v4-flash`: an intentional choice as a
**minimum clarity threshold** — if a mandate is executed correctly by the cheapest
model, the mandates are clear enough for production. For complex scenarios
use `E2E_MODEL=opencode-go/claude-sonnet-4-5 make e2e`.

The oracle is the **state of the KB on filesystem and git**, not the text produced by the agent
(operator-vs-agent principle). Every scenario is self-contained: it creates its own KB, starts
and stops its own server (trap EXIT). See [`test/e2e/README.md`](../test/e2e/README.md)
for prerequisites, flags and how to add scenarios.

#### Available E2E scenarios

| # | Name | Type | What it verifies |
|---|---|---|---|
| 01 | `01_mcp_crud` | AGENT | Basic CRUD: map_create → concept_write → concept_expand → satellite → atlas_overview |
| 02 | `02_read_write` | AGENT | search navigation + write on a pre-populated KB (kb-homelab-lite) |
| 03 | `03_config_opencode` | OPERATOR | `cartographer connect opencode` (live HTTP server) generates opencode.json + materializes bundled skills via `sync_pull` |
| 04 | `04_skill_lifecycle` | OPERATOR | skill materialized with `connect --auto-trust`, pruned by `sync` after removal from the KB |
| 05 | `05_sync_drift` | OPERATOR | `cartographer status` exits 0 (in-sync) / 1 (drift); `sync --auto-trust` realigns |
| 06 | `06_governance` | AGENT | supersede writes `status: superseded` + `superseded_by` in the frontmatter |
| 07 | `07_multikb_auth` | AGENT | multi-KB + auth: concept in kbA, not in kbB; 401 without token |
| 08 | `08_git_multiclone` | OPERATOR | git as sync: write on one clone visible on the other after SyncIn (fetch/pull) |
| 09 | `09_git_conflict` | OPERATOR | rebase conflict detected: `.cartographer/conflicts.json` populated, concept `degraded`, `conflicts_list` lists it |

The OPERATOR scenarios (03, 04, 05, 08, 09) require no LLM credentials and can run in CI —
03/04/05 still start a local HTTP server: the client always talks to the server over HTTP
(see `decisions.md`), there is no longer a filesystem-only channel.
The AGENT scenarios (01, 02, 06, 07) require OpenCode, `E2E_LLM_BASE_URL` and an LLM model.

---

## Pre-release checklist

Before every release verify:

- [ ] `make vet && make test` green
- [ ] `make smoke` green (stdio)
- [ ] `make smoke-http` green (HTTP operator)
- [ ] Manual agent-level test on at least one KB: `map_create` → `concept_write` → `concept_expand` → `search` → `validate`
- [ ] `make e2e-quick` green (scenario 01_mcp_crud with deepseek-v4-flash)
- [ ] `cartographer connect --dry-run` emits the expected files for all providers
- [ ] The `kb-create` skill correctly guides the creation of a KB from scratch

---

## Reference files

| File | Role in testing |
|---|---|
| `scripts/test-kb-flow.sh` | Operator-level HTTP smoke (direct curl) |
| `Makefile` → `smoke`, `smoke-http` | CI entry points |
| `internal/mcpserver/server_test.go` | In-process unit tests of MCP tools |
| `internal/skillbundle/bundled/kb-create/SKILL.md` | Guided skill for manual agent-level tests |
| `test/e2e/run.sh` | Agent-level E2E orchestrator (headless OpenCode) |
| `test/e2e/README.md` | Prerequisites, flags, scenario list, how to add one |
| `test/e2e/lib/server.sh` | server_start (E2E_AUTH/E2E_TOKENS) / server_wait_health / server_stop |
| `test/e2e/lib/kb.sh` | kb_make / kb_copy_fixture |
| `test/e2e/fixtures/kb-homelab-lite/` | Pre-populated homelab fixture (infra map, network expanded concept + skill) |
| `test/e2e/scenarios/*.sh` | The scenarios — list and description in the §E2E scenarios table above |
