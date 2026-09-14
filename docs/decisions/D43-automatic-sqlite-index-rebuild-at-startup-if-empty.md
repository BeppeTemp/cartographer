---
topic: control-plane
---

# D43 — Automatic SQLite index rebuild at startup if empty, without embedding

**Decision.** `<kb>/.cartographer/index.db` is gitignored and derived: after a fresh clone (e.g.
k8s pod restart) it starts empty and `search` answered `count=0` until someone called
`index_rebuild` manually. At startup, for each mounted KB, if `ix.Count() == 0` the server rebuilds
FTS5 from the `.md` files (`mcpserver.EnsureSQLIndexFresh`, reuses `rebuildSQLIndex`), deliberately
**without embedding**. Best-effort: an error logs and does not prevent startup.
**Rationale.** No embedding at boot because Ollama may be slow/absent — blocking
startup on an optional external dependency would be the wrong coupling; the cache
repopulates anyway at the first `index_rebuild`/semantic search. `COUNT(*)==0` is the cheapest
signal and consistent with the "derived and rebuildable index" invariant.
Details: `docs/control-plane.md` §Search index.
