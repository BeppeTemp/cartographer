# Deterministic end-to-end tests

These scenarios exercise the built binary through its real HTTP and CLI
boundaries. Assertions use filesystem state, git state, HTTP responses and
exit codes. No LLM, agent client or external model endpoint is involved.

They run in GitHub Actions after the Go test suite.

## Run

```bash
make e2e
make e2e-upgrade
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
| `16_deprecated_prefix_keys` | D288, D325 removed topology keys: a config still carrying `mount_mode`/`tool_prefix_mode` starts silently, tools are never prefixed, the routed mount is served, and a KB named like the routed endpoint is skipped with a warning |
| `17_routed_multikb` | D187, D288 routed topology: `/health` advertises it, `/mcp/routed` lists each tool once, `kb` routing and refusals, one MCP entry per provider with `?kbs=` carrying the binding, `kb` required without enum, single-sync migration from per-KB entries |
| `18_workspace_projection` | D193 workspace-scoped projection: each workspace receives only its own KB's same-named skill, bundled skills stay global, unbind prunes one workspace, generated files do not dirty the repository, an unbound workspace is fail-closed and read-only |
| `19_hero_publish` | D790 the README hero's orphan branch: `publish-hero.sh` leaves one commit with the `Web-Tree:` trailer, `hero-needed.sh` answers by the tree hash of `web/` |
| `20_origin_and_listen` | D906 fixed server rules: cross-origin requests refused, a hidden tool served when named, `serve --http 0.0.0.0` with auth off refuses to start, `serve` on a non-KB folder fails naming `kb create` |
| `21_forge_rules` | D996 a remote that enforces membership (`test/forge/pre-receive.sh`): a write naming its author lands as author and committer, one without is refused and stays local, `git.identity` publishes, a commit with no writer is re-committed by it; the whole suite runs with no git identity |
| `22_workspace_derived_binding` | D1019 a repository a KB declares in its `paths.yaml` is bound to that KB: `sync` projects into it, a `bind: false` repository receives nothing, `workspace list` shows the source, the bridge serves write tools, tracked `CLAUDE.md` is untouched, an explicit `--no-kb` binding replaces the derived one |
| `23_film_publish` | D1068 the README film's orphan branch: `film/publish.sh` leaves one commit with the `Film-Tree:` trailer, `film/film-needed.sh` answers by the hash of the film's inputs (`film/inputs.sh`) |
| `24_generic_mcp_client` | D1102 a host with no Cartographer client side, with curl only: `/health` and the tool surface follow the token, the guide in `initialize` and `kb_guide`, a first page accepted, authorship, non-disclosure, no doctor nudge and no CLI command for a generic connection |
| `25_search_section` | D1207 a `search` hit names the `##` section that matched and `concept_read` reads it back smaller than the page; a journal entry and a type listed in the map's `read_whole` carry `read: "whole"` and no section |
| `26_upgrade_from_previous` (optional) | #1173 a client of the previous stable release, fully installed and approved, switched to this build: one sync asks no approval again, leaves no file under a retired name and no dangling local path, a second sync writes nothing and `status` is in sync, a revoked hook stays revoked |

The numbering is stable, so the gaps are intentional: model behavior is
evaluated during real usage, not in the repository's deterministic CI.

## Optional scenarios

`optional/` holds the scenarios the default loop skips because they cost more than
the whole suite. `--only <name>` finds them. `26_upgrade_from_previous` builds the
previous release from a throwaway worktree (`make e2e-upgrade`; the release PR's
`upgrade-e2e` job): `E2E_UPGRADE_FROM=<ref>` picks another tag, `E2E_UPGRADE_ONLINE=1`
lets the build download modules the cache lacks. It needs the tags (`git fetch --tags
--unshallow` in a shallow clone).

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
    25_search_section.sh
  optional/
    26_upgrade_from_previous.sh
```

Each scenario creates its own directory under `E2E_TMP_DIR`, owns the server
processes it starts and registers cleanup through `trap`.

When invoking client commands, set `HOME` to the scenario sandbox. Client
configuration is machine-wide; an unisolated invocation could modify the
developer or CI runner's real home directory.

For the complete testing policy, see
[`docs/testing.md`](../../docs/testing.md).
