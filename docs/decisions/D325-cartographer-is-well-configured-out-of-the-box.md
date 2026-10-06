---
topic: control-plane
---

# D325 — Out of the box, what keeps a KB healthy is on: new maps demand index entries, a fresh install keeps an audit log that rotates, a client with no reliable hook gets its sync timer, a new KB gets an `instructions.md`

**Decision.** `map_create` applies `require_index_entry: true` when the caller omits the key (only an explicit `false` opts out). `service install` writes `audit.log: <data>/audit.log` into the generated `server.yaml`, and `audit.retention_days` defaults to 90 (an explicit `0` keeps everything).

`setup` and `connect` install the sync timer themselves when `providersNeedingSyncTimer` is non-empty (a connected provider with no session hook, or Kiro's partial one), revising D140's opt-in. `setup` lists it in its plan; `service sync-timer uninstall` is the explicit opt-out, stored as `sync_timer_opt_out` in `.cartographer.yaml` and cleared by `install`; `disconnect` removes the timer when no remaining client needs it, without recording an opt-out. `kb create` (`kb.Init`) writes a short generic `instructions.md` (status convention, write discipline) when it creates the KB.

**Why.** An audit of every setting found the defaults right except these: operators turned the first on by hand on every mature KB, and an audit log that is absent on a fresh install or never rotates is an ops problem the operator discovers late. Signing (`audit.key_seed`) stays opt-in: it needs a secret the server cannot invent.

**Alternatives rejected.**
- Default applied in `kb.CreateMapWithContract` (a `*bool` or "zero means true" there): a bool zero value cannot tell "unset" from "false", and `kb.CreateMap` callers (tests, provisioning) rely on a bare contract; the MCP handler is where absence is observable, so the default lives there.
- The timer left as a hint: a client whose only sync path is a hint the operator forgets drifts silently. The "background job as a side effect" objection is met by showing it in the plan and by removing it when unused.
- `instructions.md` written on every `Init`: it would add an untracked file to an existing KB (the same reason `data/.gitignore` is creation-only).
- Retention layered by "non-zero wins": it would make an explicit `retention_days: 0` impossible. `rawAudit.RetentionDays` is a pointer instead.

**Consequences.** Maps created before this change are untouched; the changelog calls out the new default for new maps. A config that omits `retention_days` now deletes checkpointed segments older than 90 days. Machines with a hook-less client gain a background timer on their next `connect` (the plan shows it; `service sync-timer uninstall` removes it for good); new KBs gain an `instructions.md`, existing ones are unchanged. The `disconnect` check uses the pure hookless predicate, not `providersNeedingSyncTimer`, which returns nil while the timer is installed. The default file carries no `preamble: none` directive, which would drop the generated operational bullets.

**WP4 — the D288 knobs are removed (gate waived).** `mcp.mount_mode`, `mcp.tool_prefix_mode`, `kbs[].tool_prefix`, `CARTOGRAPHER_MCP_MOUNT_MODE`, `CARTOGRAPHER_MCP_TOOL_PREFIX_MODE` and the `--mount-mode` flag are gone, with the `serve` warnings and the dead `config.ResolveToolPrefix` / `ValidateToolPrefixUniqueness` code (`SanitizeToolPrefix` and `ValidateToolPrefixShape` stay: legacy-tool-name handling and the client's reading of an older server's `/health` still use them). The plan gated this on one release (v0.19.0) carrying the D288 warnings; the maintainer waived that, so the deprecation and the removal ship in the same release. The cost is a breaking-ish config change: a YAML key left in a config is **silently ignored** (unknown keys do not fail the load, and nothing warns), but a launcher still passing `--mount-mode` fails with an unknown-flag error and the env vars are no longer read. Both knobs were already no-ops since D288, so no behaviour changes for a deployment that did not rely on the flag.
