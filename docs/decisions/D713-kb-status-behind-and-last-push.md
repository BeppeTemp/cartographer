---
topic: control-plane
---

# D713 — kb_status reports behind, last push and last sync-in

**Decision.** `kb_status` and `sync_status` add `behind_commits`, `last_push_at` and `last_sync_in_at`, always present, `null` when unknown. Behind is computed against the local remote-tracking ref as the last fetch left it, and `last_sync_in_at` says how fresh that is. `last_push_at` is in-process only.

**Why.** An agent forbidden to run git in the clone could see ahead but not behind. The status tools stay read-only with no network (D145), so a count is only honest next to its as-of time. Persisting the push time would need new on-disk state for little gain.

**Alternatives rejected.** Fetching on read: breaks the no-network invariant. Persisting `last_push_at` (reflog parsing): new state, out of scope. Storing it inside `GitStatus`: `setGitStatus` replaces the whole struct, so the next failure would erase it, hence a separate KB field. Comparing against the server profile's base branch: out of scope; behind uses the same ref as ahead.

**Consequences.** `last_push_at` is `null` after a restart until the next push. The server profile records the push before the forge step, since a push that succeeded is a push. `clearDivergedStatus` is not a push and never records one. The change is additive.
