---
topic: concurrency-git
---

# D94 — Git-history changes digest

**Status: implemented (2026-07-24).**

**Decision.** `changes_since` is a read-only, server-side digest over Git history. The caller supplies an RFC3339 timestamp or `<N>d`/`<N>h` duration; no per-client last-seen state is stored. File changes aggregate by concept with newest-status-wins, including renames under their destination ID. `.cartographer/` is ignored, while logs, maps and provisioning artifacts remain only in the `other_changes` count.

**Rationale.** Every MCP write already creates a Git commit, so Git supplies the authoritative timestamp, author and concept-level history without introducing client state. A compact per-concept aggregate answers a return-to-work digest without exposing raw commit noise.
