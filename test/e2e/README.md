# E2E agent-level tests — Cartographer

Harness that drives **OpenCode in headless mode** as a real agent against a local Cartographer server.
The test oracle is the **KB state on filesystem and git**, not the text produced by the agent.

---

## Operator vs agent model

| Role | Who performs it | Tools available |
|---|---|---|
| **Operator** | `run.sh` (bash) | `make build`, filesystem, curl, git, processes |
| **Agent** | `opencode run` (headless) | Only the Cartographer server's MCP tools |

The runner does not help the agent on a privileged channel during the task: the agent receives a
mandate in natural language and must complete it autonomously using only the MCP tools. This verifies
the agent's **real cognitive capability**, not just server correctness.

---

## Prerequisites

- **OpenCode** installed: `/opt/homebrew/bin/opencode` (or `OPENCODE_BIN=<path>`)
- **OpenAI-compatible LLM endpoint**: `E2E_LLM_BASE_URL` (e.g. `https://api.example.com/v1`) —
  required only by AGENT scenarios; without it, OPERATOR scenarios still run
- **Model** available at that endpoint: default `opencode-go/deepseek-v4-flash` (minimum clarity gate)
- Working `make build` (produces `bin/cartographer`)

---

## How to run

```bash
# All scenarios (default model)
E2E_LLM_BASE_URL=https://api.example.com/v1 make e2e

# Only the basic CRUD scenario
E2E_LLM_BASE_URL=https://api.example.com/v1 make e2e-quick

# Scenarios with a different model
E2E_LLM_BASE_URL=... E2E_MODEL=opencode-go/claude-sonnet-4-5 make e2e

# Direct flags
./test/e2e/run.sh --only 01_mcp_crud --keep --model opencode-go/gpt-4o

# Operator-only scenarios (no agent — useful in CI without LLM credentials)
./test/e2e/run.sh --only 03_config_opencode
./test/e2e/run.sh --only 04_skill_lifecycle
./test/e2e/run.sh --only 05_sync_drift
```

Flags available for `run.sh`:

| Flag | Description |
|---|---|
| `--only <name>` | Runs only the given scenario (without the `.sh` extension) |
| `--keep` | Does not remove the temporary directory after the run (useful for debugging) |
| `--model <m>` | Overrides `E2E_MODEL` for this run |

---

## Structure

```
test/e2e/
  run.sh                  # Orchestrator: build, tmp dir, scenarios, report
  lib/
    server.sh             # server_start (supports E2E_AUTH/E2E_TOKENS) / server_wait_health / server_stop
    kb.sh                 # kb_make / kb_copy_fixture — KB setup helpers
    sandbox.sh            # sandbox_create / sandbox_create_auth — generates opencode.jsonc
    agent.sh              # agent_run: invokes opencode headless
    assert.sh             # assert_dir_exists / assert_file_exists / assert_file_contains /
                          # assert_git_log_nonempty / assert_concept_exists
  fixtures/
    kb-empty/.gitkeep     # Placeholder no longer used directly (kept for compatibility)
    kb-homelab-lite/      # Pre-populated "homelab" fixture: infra/network archive + domain skill
  scenarios/
    01_mcp_crud.sh        # AGENT     — Basic CRUD: map_create → concept_expand → concept_write
    02_read_write.sh      # AGENT     — Read+write on kb-homelab-lite (search → concept_write)
    03_config_opencode.sh # OPERATOR  — cartographer connect opencode (HTTP, via sync_pull)
    04_skill_lifecycle.sh # OPERATOR  — skill materialization + prune after removal (connect/sync)
    05_sync_drift.sh      # OPERATOR  — drift detection with `cartographer status` (exit 0/1)
    06_governance.sh      # AGENT     — supersede + validate (checks frontmatter marker)
    07_multikb_auth.sh    # AGENT     — multi-KB + auth bearer token + KB isolation
    08_git_multiclone.sh  # OPERATOR  — git as sync: write on one clone visible on the other
    09_git_conflict.sh    # OPERATOR  — rebase conflict detection + registry + degraded
```

---

## Scenarios

| # | Name | Type | What it checks |
|---|---|---|---|
| 01 | `01_mcp_crud` | AGENT | Basic CRUD: map → expanded concept → concept |
| 02 | `02_read_write` | AGENT | Navigation (search) + write on a pre-populated KB |
| 03 | `03_config_opencode` | OPERATOR | `connect` generates `opencode.json` with the correct schema and materializes the skill via HTTP |
| 04 | `04_skill_lifecycle` | OPERATOR | skill materialized with `connect --auto-trust`, pruned by `sync` after removal from the KB |
| 05 | `05_sync_drift` | OPERATOR | `status` exits 0 (in-sync) or 1 (drift) correctly |
| 06 | `06_governance` | AGENT | supersede writes `status: superseded` + `superseded_by` in the frontmatter |
| 07 | `07_multikb_auth` | AGENT | multi-KB with auth: concept in kbA, not in kbB; 401 without token |
| 08 | `08_git_multiclone` | OPERATOR | git as sync: write on one clone visible on the other after SyncIn |
| 09 | `09_git_conflict` | OPERATOR | rebase conflict: `conflicts.json` populated, `degraded` concept, `conflicts_list` |

The **OPERATOR** scenarios (03, 04, 05, 08, 09) do not invoke the agent and can run without LLM
credentials (03/04/05 still start a local HTTP server: the client always talks to the server over
HTTP, see `decisions.md` — there is no longer a filesystem-only channel).
The **AGENT** scenarios (01, 02, 06, 07) require OpenCode and an LLM model.

---

## Architecture: self-contained scenario

Each scenario is **self-contained**: it creates its own KB (in `$E2E_TMP_DIR/<scenario>/`), starts and
stops its own server via a `trap EXIT`, runs the assertions and prints `[SCENARIO <name>] PASS|FAIL`.
The orchestrator (`run.sh`) does not manage servers — it only handles build, tmp dir and result collection.

```
run.sh
  ├── make build
  ├── mkdir E2E_TMP_DIR
  └── for each scenario:
        bash scenario.sh
          ├── kb_make / kb_copy_fixture
          ├── server_start (AGENT/HTTP scenarios only)
          ├── trap EXIT → server_stop
          ├── [agent_run] (AGENT scenarios only)
          ├── assert_*
          └── PASS/FAIL
```

---

## How to add a scenario

1. Create `test/e2e/scenarios/NN_name.sh` (increasing numbering for ordering).
2. Source `assert.sh`, `kb.sh`, `server.sh` (if the server is needed), `sandbox.sh` + `agent.sh` (if agent-based).
3. Create the KB in `${E2E_TMP_DIR}/${SCENARIO_NAME}/` with `kb_make` or `kb_copy_fixture`.
4. If server-based: `server_start`, `server_wait_health`, `trap 'server_stop' EXIT`.
5. If agent-based: `sandbox_create` + `agent_run` (never invoke `opencode run` directly).
5-bis. If the scenario invokes the **client** (`connect`/`status`/`sync`): always prefix
   `HOME="$SANDBOX_DIR"` before `$BIN`. The client writes machine-wide to `$HOME`
   (`clientconfig.TargetDir`): without the override, the scenario would overwrite the
   machine's real configuration (`~/.cartographer.yaml`, `~/.claude/`, `~/opencode.json`).
6. Assertions on the oracle (filesystem + exit code): NOT on the agent's text.
7. Exit with `0` if `E2E_FAILURES -eq 0`, `1` otherwise.
8. `run.sh` will pick it up automatically in lexicographic order.

---

## Model choice

The default gate is `opencode-go/deepseek-v4-flash`: a cheap, fast model used as a
**minimum clarity threshold** — if a mandate is clear enough to be executed correctly
by deepseek-v4-flash, it is fit for production. For more complex scenarios use a
more capable model via `--model` or `E2E_MODEL`.

For the full testing strategy see [`docs/testing.md`](../../docs/testing.md).
