---
topic: control-plane
---

# D135 — Remove semantic search and the Ollama embedding backend

**Decision.** Semantic and hybrid search are removed, along with `internal/embed`, the
`embeddings` half of the persisted index, the `search.ollama_*` configuration, the
`--ollama` flag and the `CARTOGRAPHER_OLLAMA*` environment variables. `search` accepts
`query`, `scope` and `limit`; `mode` and `use_semantic` are gone from the input schema and a
call passing either is rejected with an error naming the removal, rather than silently
downgraded to keyword results. Three invariants hold: keyword behaviour is unchanged (the
FTS5 trigram path, the all-terms-then-any-term retry, the `title`/`snippet` enrichment of
D70, and the reported `keyword_fts5`/`keyword` mode values); `search` keeps its `query` and
`scope` semantics; and a database that still carries an `embeddings` table opens and works
untouched — the table is simply never read or written again, and `CREATE TABLE` is dropped
from schema init so new databases never get one.

**Rationale.** The capability was opt-in from the start and the only deployment that ever
enabled it removed the backend the same day: a full rebuild against a GPU-backed Ollama
OOM-killed the container at both 2 and 3 GiB and persisted 0 of 142 embeddings. It was not
free while disabled either — a whole package, a second input schema, a duplicated handler,
four accessor methods on the persisted index, four configuration knobs, and a `search`
contract advertising two modes that error out at call time on every deployment. Removing it
rather than deprecating it is the right call at 0.x: there is no supported deployment to
stay compatible with, and rejecting the removed arguments makes a stale client fail loudly
instead of quietly getting results it did not ask for.
