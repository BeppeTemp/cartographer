---
topic: control-plane
---

# D1595 — The UI API drops the git blocks and /contradictions, and caches births by HEAD

**Decision.** `/api/ui/v1/kbs/{kb}/overview` and `/api/ui/v1/settings` carry no `git` block, and `/api/ui/v1/kbs/{kb}/contradictions` is removed (404). `/maintenance/summary` stays, with a note in `docs/control-plane.md` that its consumer lives on `exp/0.28` (D1455). `/births` keeps the result of the history walk per KB root and HEAD SHA; the visibility filter runs after the cache.

**Why.** The Atlas never read the `git` blocks, yet every overview load, KB switch, Map change and live refresh spawned `git remote get-url`, and every settings read added `GitStatusSnapshot`. `/contradictions` has had no client since 0.25. `/births` walked the whole history with `git log --name-status` at each Replay click or export, repeatable by any reader; births change only with a new commit.

**Alternatives rejected.** Keeping the `git` blocks with corrected types (`number | null`): it keeps the subprocess for a block nobody reads. Declaring `/contradictions` public API: it would freeze a route with no consumer. A birth cache keyed by revision counter rather than HEAD: HEAD is the exact input of the walk.

**Consequences.** `kb_status` still reports replication facts to agents; only the Atlas routes lost them. A client of the removed route gets 404. The births cache is per process and unbounded in the number of KBs mounted, one entry each.
