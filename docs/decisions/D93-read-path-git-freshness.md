---
topic: concurrency-git
---

# D93 — Read-path Git freshness

**Status: implemented (2026-07-24).**

**Decision.** Every read-only MCP tool piggybacks `SyncIn` before serving a Git-synchronised KB. The existing `git.in_window` / `CARTOGRAPHER_SYNC_IN_WINDOW` freshness window limits both reads and writes to at most one fetch per KB per window. A pull that moves HEAD uses D90's reconciliation callback before the handler runs. A read-side rebase conflict is registered and marked degraded, while other sync failures are logged; either failure still serves the current local tree.

**Rationale.** Piggybacking avoids a per-KB background poller and its lifecycle/conflict-context complexity while bounding remote work with the existing window. Reads must stay available during a remote or rebase failure, so stale/degraded local content is preferable to turning an otherwise valid read into an MCP error.
