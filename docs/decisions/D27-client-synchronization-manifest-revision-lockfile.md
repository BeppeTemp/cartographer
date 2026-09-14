---
topic: sync-provisioning
---

# D27 — Client synchronization: manifest+revision, lockfile, layered triggers

**Decision.** Client realignment (skill/hook/agent) is based on three objects: a
server-side **provisioning manifest** (`revision` = aggregate hash), a client-side
**lockfile** (applied state + `managed[]`), and a `ProvisionedArtifact` abstraction with an
extensible `kind`. Layered triggers (`SessionStart` hook, `sync_check`/`sync_apply` tools,
optional MCP push), default **notify + signature gate**, **managed-only prune**.
**Rationale.** A pull-based model with fingerprint is O(1) and cross-provider on a
heterogeneous request/response transport; the `kind` abstraction avoids redesigning for hooks/subagents.
Skills are executable code: the signature gate aligns sync with the supply-chain invariants.
*(Superseded state: Layer 3 and codex/kiro providers → D34; `cartographer-configure` removed in
favor of `cartographer connect/status/sync` → D37/D40.)*
Details: `docs/sync.md`.
