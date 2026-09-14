---
topic: sync-provisioning
---

# D60 — Client-side bootstrap hook: auto-sync at session start (WP4)

**Context.** `docs/sync.md` §Layer 1 described, since before D57, a `SessionStart` hook that
invokes `cartographer sync`/`status` at session start — never implemented: D57/D58/D59 built
the registration mechanism for *KB* hooks, but this one never comes from a KB. An
artifact generated entirely by the client was needed.

**Decision.** `internal/provisioning/bootstrap.go` introduces `EnsureBootstrapHook`: it materializes
a deterministic script (`bootstrap.sh`, calls `cartographer sync --auto-trust`) and reuses
verbatim `registerHookSettings`/`registerHookConfigTOML`/`registerOpenCodePlugin` (D57/D58/D59) for
native registration — zero new code. Reserved name `cartographer-bootstrap`. Called by
`doConnect`/`cmdSync` before the manifest fetch, independent of server reachability.
**"Orphan" protection**: the server manifest will never contain this artifact — `ComputeDiff`
explicitly excludes `Kind=="hook" && Name==BootstrapHookName` from the `Removed` computation, so the
bootstrap is not deleted and recreated at every sync (same principle already used for `kind:
instructions`). Name collision with a same-named KB hook → ignored with a warning, never overwritten.
`kiro` (no native hook mechanism) remains a no-op, degraded to Layer 2.
**Rationale.** Protecting the bootstrap from the diff (instead of having it reappear at every sync) avoids
noise (files/config rewritten every round) and a window in which the hook does not exist between prune and
rematerialization.
Details: `docs/sync.md` §Layer 1.
