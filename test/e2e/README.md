# Deterministic end-to-end tests

These scenarios exercise the built binary through its real HTTP and CLI
boundaries. Assertions use filesystem state, git state, HTTP responses and
exit codes. No LLM, agent client or external model endpoint is involved.

They run in GitHub Actions after the Go test suite.

## Run

```bash
make e2e
./test/e2e/run.sh --only 10_scoped_tokens
./test/e2e/run.sh --only 09_git_conflict --keep
```

`--keep` preserves the temporary directory for debugging. `E2E_HTTP_PORT`
overrides the default base port.

## Scenarios

| Scenario | Boundary covered |
|---|---|
| `03_config_opencode` | HTTP client connection, OpenCode config and bundled-skill materialization |
| `04_skill_lifecycle` | Provisioning apply and managed prune |
| `05_sync_drift` | Client drift detection and reconciliation |
| `08_git_multiclone` | Remote pull/rebase visibility across clones |
| `09_git_conflict` | Conflict registry, degraded marker and conflict listing |
| `10_scoped_tokens` | Per-KB `r`/`rw` scope enforcement |
| `11_signed_provisioning` | Signed artifact verification and fail-closed unpinned rotation |
| `12_mcp_approval` | D115 allow-list, point approval, stale hash rejection and revoke/prune |
| `13_stdio_mcp` | D116 trusted stdio MCP lifecycle across all provider configurations |
| `14_rbac_visibility` | D118 fine-grained RBAC: per-KB collection visibility, exact-resource non-disclosure, out-of-perimeter write denial, admin/legacy retrocompatibility |
| `15_operational_audit` | D119 compliance audit: attempt/completion event pairs, chain verification, export, tamper detection |

The numbering is retained to preserve historical references. Removed gaps were
LLM-driven scenarios; model behavior is evaluated during real usage, not in the
repository's deterministic CI.

## Structure

```text
test/e2e/
  run.sh
  lib/
    assert.sh
    kb.sh
    server.sh
  fixtures/
    kb-homelab-lite/
  scenarios/
    03_config_opencode.sh
    04_skill_lifecycle.sh
    05_sync_drift.sh
    08_git_multiclone.sh
    09_git_conflict.sh
    10_scoped_tokens.sh
    11_signed_provisioning.sh
    12_mcp_approval.sh
    13_stdio_mcp.sh
    14_rbac_visibility.sh
    15_operational_audit.sh
```

Each scenario creates its own directory under `E2E_TMP_DIR`, owns the server
processes it starts and registers cleanup through `trap`.

When invoking client commands, set `HOME` to the scenario sandbox. Client
configuration is machine-wide; an unisolated invocation could modify the
developer or CI runner's real home directory.

For the complete testing policy, see
[`docs/testing.md`](../../docs/testing.md).
