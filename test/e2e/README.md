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
| `12_mcp_approval` | D115 point approval, stale hash rejection and revoke/prune |
| `13_stdio_mcp` | D116 trusted stdio MCP lifecycle across all provider configurations |
| `14_rbac_visibility` | D118 fine-grained RBAC: per-KB collection visibility, exact-resource non-disclosure, out-of-perimeter write denial, admin/legacy retrocompatibility |
| `15_operational_audit` | D119 compliance audit: attempt/completion event pairs, chain verification, export, tamper detection |
| `19_hero_publish` | D790 the README hero's orphan branch: `publish-hero.sh` leaves one commit with the `Web-Tree:` trailer, `hero-needed.sh` answers by the tree hash of `web/` |
| `23_film_publish` | D1068 the README film's orphan branch: `film/publish.sh` leaves one commit with the `Film-Tree:` trailer, `film/film-needed.sh` answers by the hash of the film's inputs (`film/inputs.sh`) |
| `21_forge_rules` | D996 a remote that enforces membership (`test/forge/pre-receive.sh`): a write naming its author lands as author and committer, one without is refused and stays local, `git.identity` publishes, a commit with no writer is re-committed by it; the whole suite runs with no git identity |
| `16_deprecated_prefix_keys` | D288, D325 the removed topology keys (`mount_mode`, `tool_prefix_mode`) are silently ignored: the server starts, tools are never prefixed |
| `17_routed_multikb` | D187, D288 the routed topology: tools advertised once, a payload far smaller than per-KB mounts, `kb` selecting the target, the binding in the URL (`?kbs=`) |
| `18_workspace_projection` | D193 workspace-scoped artifact projection: two KBs with a same-named skill, each provider bound to a different KB per workspace |
| `20_origin_and_listen` | D906 the fixed server rules: cross-origin refused, a hidden tool served when named, no non-loopback listen without auth, `serve` on a non-KB folder fails |
| `22_workspace_derived_binding` | D1019 a repository a KB declares in `paths.yaml` is bound to that KB without a `workspace bind`; one marked `bind: false` receives nothing |
| `24_generic_mcp_client` | D1102 a host with no Cartographer client side, with curl only: `/health` and the tool surface follow the token, the guide in `initialize` and `kb_guide`, a first page accepted, authorship, non-disclosure, no doctor nudge and no CLI command for a generic connection |

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
    16_deprecated_prefix_keys.sh
    17_routed_multikb.sh
    18_workspace_projection.sh
    19_hero_publish.sh
    20_origin_and_listen.sh
    21_forge_rules.sh
    22_workspace_derived_binding.sh
    23_film_publish.sh
    24_generic_mcp_client.sh
```

Each scenario creates its own directory under `E2E_TMP_DIR`, owns the server
processes it starts and registers cleanup through `trap`.

When invoking client commands, set `HOME` to the scenario sandbox. Client
configuration is machine-wide; an unisolated invocation could modify the
developer or CI runner's real home directory.

For the complete testing policy, see
[`docs/testing.md`](../../docs/testing.md).
