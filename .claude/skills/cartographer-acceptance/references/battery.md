# The acceptance battery of lane B

What the tester of `cartographer-acceptance-sandbox` tries on a candidate in an isolated HOME,
one row per item. The last column says what automates the row: a scenario or a Go test of this
repository, or `tester` for an item only an agent can judge. A row that has a path is run by CI:
the train gate runs `test/e2e/scenarios/` and the Go tests; the scenarios of `test/e2e/optional/` run
in the nightly from the `nightly-acceptance` change on, and until then by hand with `make e2e-nightly`.
The tester therefore spends its time on `tester` rows and on the entries of the matrix. `TestAcceptanceBattery`
(`internal/repodocs`) fails when a path does not exist or when an optional scenario is named by no
row: add the row in the same change as the scenario.

Placeholder names only (`kb-a`, `example.com`, `user`).

## Base battery

| id | item | automated by |
|---|---|---|
| B01 | KB lifecycle: create, list, rename, unmount | `cmd/cartographer/kbcmd_test.go`, `cmd/cartographer/kbrename_test.go`, `cmd/cartographer/kbunmount_test.go` |
| B02 | Tokens: `r`, `rw` and admin scopes enforced per KB | `test/e2e/scenarios/10_scoped_tokens.sh` |
| B03 | An MCP session over HTTP: initialize, tools, one write and one read | `test/smoke/http.sh` |
| B04 | The writes the gate must refuse, refused with the check named | `internal/mcpserver/writegate_test.go` |
| B05 | Git push, divergence and conflict resolution | `test/e2e/scenarios/09_git_conflict.sh`, `test/e2e/scenarios/08_git_multiclone.sh` |
| B06 | Every Atlas read endpoint answers | `internal/webui/webui_test.go`, `web/e2e/flows.spec.ts` |
| B07 | Client cycle per provider: `connect`, then `sync` twice, the second a no-op | `test/e2e/scenarios/05_sync_drift.sh`, `test/e2e/scenarios/03_config_opencode.sh` |
| B08 | Client cycle: `status` in sync and in drift | `cmd/cartographer/status_test.go` |
| B09 | Client cycle: `disconnect` leaves no file | `cmd/cartographer/disconnect_test.go` |
| B10 | Client cycle: `setup` | `cmd/cartographer/setup_test.go` |
| B11 | `workspace bind` and `unbind` | `test/e2e/scenarios/18_workspace_projection.sh` |
| B12 | The symlink opt-in | `internal/provisioning/allow_symlinks_test.go` |
| B13 | Artifacts with signing and re-approval | `test/e2e/scenarios/11_signed_provisioning.sh`, `test/e2e/scenarios/12_mcp_approval.sh` |
| B14 | Secrets with a real key, a missing one and a wrong one | `test/e2e/optional/32_secrets_lifecycle.sh` |
| B15 | A test plugin: validate, install, approve, update with re-approval, remove | `cmd/cartographer/plugin_lifecycle_test.go` |
| B16 | A plugin's commands and tools | `test/e2e/scenarios/28_plugin_scripts.sh` |
| B17 | The CLI without a TTY | `cmd/cartographer/bare_test.go` |
| B18 | `--help` exits 0 and `connect`, `disconnect`, `status`, `sync` and `doctor` are documented in `docs/configurator.md` | `test/e2e/optional/34_server_down_exit_codes.sh` |
| B19 | `update check` never offers a candidate | `test/e2e/optional/33_update_check_candidates.sh` |
| B20 | `status` and `sync` with the server down: exit codes and messages | `test/e2e/optional/34_server_down_exit_codes.sh` |
| B21 | The upgrade from the previous release: one sync, no new approval, no drift | `test/e2e/optional/26_upgrade_from_previous.sh` |
| B22 | The messages a user reads on a first run, in plain words | tester |

## Adversarial battery

| id | item | automated by |
|---|---|---|
| A01 | Odd concept ids | `test/e2e/optional/31_adversarial_protocol.sh` |
| A02 | Frontmatter that is not a mapping | `test/e2e/optional/31_adversarial_protocol.sh` |
| A03 | A body over 1 MiB | `test/e2e/optional/31_adversarial_protocol.sh` |
| A04 | Conflicting batches | `test/e2e/optional/31_adversarial_protocol.sh` |
| A05 | Ten parallel writers on one concept | `test/e2e/optional/31_adversarial_protocol.sh` |
| A06 | Negative and huge limits | `test/e2e/optional/31_adversarial_protocol.sh` |
| A07 | Unknown parameters | `test/e2e/optional/31_adversarial_protocol.sh` |
| A08 | Malformed and batched JSON-RPC | `test/e2e/optional/31_adversarial_protocol.sh` |
| A09 | A lockfile entry that leaves the client's folder | `internal/provisioning/readlock_test.go` |
| A10 | An instruction planted in a concept does not steer the agent | tester |

## Smoke battery

| id | item | automated by |
|---|---|---|
| S01 | Create a KB with a remote | `cmd/cartographer/kbcmd_test.go` |
| S02 | Serve with a token, one write and one refusal | `test/e2e/scenarios/10_scoped_tokens.sh` |
| S03 | `connect` and `sync` twice | `test/e2e/scenarios/05_sync_drift.sh` |
| S04 | `/health` and one Atlas endpoint | tester |
