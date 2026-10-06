---
topic: data-plane
---

# D322 — `archived` is a reserved status: closed journal entries are harvested, then leave the active surface

**Decision.** `archived` joins `deprecated` and `superseded` as a status the server knows (`kb.IsRetired`, `closedPhase`). An archived concept stays readable and linkable but is out of the default `search` (with a marked fallback when nothing live matches, and `include_archived`), out of `read_cost`, out of the `atlas_overview` structure graph and out of the `map_oversize` count. `kb_review` lists closed journal entries older than the journal's `harvest_after` (default 45 days) as `harvest_candidate`; the bundled kb-doctor skill carries their durable facts into live pages and archives them. The server also records when each concept was last handed to an agent (`.cartographer/read-access.json`), reported by `kb_status` as `read_access`. `map_update` now takes `concept_types`, `ontology_mode` and `harvest_after`.

**Why.** Closed journal entries kept the same search weight, graph weight and read cost forever; on a real KB seventy of them were archived by hand in one session. The server finds candidates deterministically (D14); which facts are durable is the agent's judgement (D305). `map_update` could not extend a strict map's type list, so a digest type could not be added after creation.

**Alternatives rejected.** Folding `archived` into the `done` synonym family: it is a lifecycle stage after done, not a vocabulary value a KB chooses. Excluding `deprecated` and `superseded` from `read_cost` too: they are few and meaningful, and changing that is a separate decision. A post-filter on search with an over-fetch: the backend's own filter drops archived hits before its cut, so the page is full and exact. A flush goroutine for the read log: a flush when a record finds the file older than five minutes needs no lifecycle; a crash loses at most that window of telemetry. A composite abandonment score now: there is no read data yet, so the signal comes first.

**Consequences.** Existing concepts already marked `archived` leave default search and read cost on upgrade. `archived` must stay out of `ValueSynonymFamilies`, and any new place that tests for "retired" calls `kb.IsRetired`. Navigation tools (`graph_*`, `concept_list`) do not record reads and still show archived concepts. A read-access entry missing means "unknown", never "never read". `link_to_retired` ignores a `digest`-typed linker, because the quarterly digest links what it archived.
