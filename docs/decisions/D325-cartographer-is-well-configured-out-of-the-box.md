---
topic: control-plane
---

# D325 — Out of the box, what keeps a KB healthy is on: new maps demand index entries, a fresh install keeps an audit log that rotates

**Decision.** `map_create` applies `require_index_entry: true` when the caller omits the key (only an explicit `false` opts out). `service install` writes `audit.log: <data>/audit.log` into the generated `server.yaml`, and `audit.retention_days` defaults to 90 (an explicit `0` keeps everything).

**Why.** An audit of every setting found the defaults right except these: operators turned the first on by hand on every mature KB, and an audit log that is absent on a fresh install or never rotates is an ops problem the operator discovers late. Signing (`audit.key_seed`) stays opt-in: it needs a secret the server cannot invent.

**Alternatives rejected.**
- Default applied in `kb.CreateMapWithContract` (a `*bool` or "zero means true" there): a bool zero value cannot tell "unset" from "false", and `kb.CreateMap` callers (tests, provisioning) rely on a bare contract; the MCP handler is where absence is observable, so the default lives there.
- Retention layered by "non-zero wins": it would make an explicit `retention_days: 0` impossible. `rawAudit.RetentionDays` is a pointer instead.

**Consequences.** Maps created before this change are untouched; the changelog calls out the new default for new maps. A config that omits `retention_days` now deletes checkpointed segments older than 90 days. The removal of the deprecated D288 knobs (`mcp.mount_mode`, `mcp.tool_prefix_mode`, `kbs[].tool_prefix` and their env vars) is gated on the v0.19.0 release and tracked in #528; the sync-timer and `instructions.md` work in the same plan is not part of this change.
