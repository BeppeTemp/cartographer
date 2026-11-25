---
topic: data-plane
---

# D2397 — A KB reaches its siblings through kb.Mounts, aliases included

**Decision.** `serve` gives every mounted KB one `kb.Mounts` value (`SetMounts`): lookup by mount name or by a `kb_aliases` old name (a mounted name wins over an alias, single hop), the current name of either (`Canonical`), the names that reach a KB (`NamesFor`) and the KB's own mount name (`MountName`, by pointer equality). A KB opened without `serve` has none.

**Why.** Features that span KBs (links, relations) need to find another KB's in-memory state without each inventing a lookup, and must keep working after `kb rename` (D772). `kb` cannot import `config`, so the aliases arrive as a plain `from -> to` map.

**Alternatives rejected.**
- A `KBByName` callback on the server dependencies (as `concept_transfer` has, D760): it lives in `mcpserver`, so the data plane cannot use it, and it knows no aliases.
- Resolving aliases in each feature: the precedence rule (mounted beats alias) would be copied and drift.

**Consequences.** The alias map is fixed at start, like `kb_aliases` ("a restart to apply"). A KB mounted twice under two names has one `MountName`, the first whose lookup is itself. Callers of `Lookup` must still apply the caller's own read grant: the mounts say what exists on the server, never what a caller may see.
