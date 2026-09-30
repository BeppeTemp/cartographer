---
topic: data-plane
---

# D278 — A source ledger of `Source` concepts revisits D28's removal of source ingestion

**Amends D28.** D28 removed `source_ingest` together with `raw/`, secret scrubbing, a
webhook exporter and content-hash provenance. This reinstates only the bookkeeping.

**Decision.** A primary source the KB has absorbed is a concept of the conventional
type `Source`, kept in a journal (`sources` by default). Two tools serve it:
`source_register` (a write: dedup by `sha256` when both sides have one, otherwise
an exact `locator`; never by title) and `source_list` (read-only, pending first,
oldest first, with a `cited_by` count). A concept cites a source by listing its
ConceptID in `provenance`; marking a source ingested is a plain `concept_patch`.
`lint` flags an `ingested` Source nobody cites (`source_uncited`), `concept_delete`
treats a citation as a link, and `kb_status` counts sources by status.

**Why.** Without a ledger a KB cannot say whether a document was already ingested,
what is still pending, where a claim came from, or what depends on a source that
turned out wrong; the same document ingested twice produces near-duplicate pages.
D28 was right about the cost, which was the pipeline, not the record.

**What stays removed from D28.** No server-side ingestion or transformation, no
mandatory copy of the source (keeping it is optional, as an asset of the expanded
concept), no scrubbing pipeline, no exporter. The server never fetches anything;
`sha256` is computed by the agent and is advisory.

**Alternatives rejected.**
- A dedicated `sources.yaml` or database: a second store outside search, graph,
  visibility, history and lint, which a concept gets for free.
- Re-adding `source_ingest`: reintroduces the pipeline D28 removed.
- Deduplicating by title: two different documents share a title far more often
  than one document is titled twice.
- A third tool to mark a source ingested: it is a frontmatter patch.
- `source_register` creating the `sources` journal: maps are created by an
  explicit act everywhere else.

**Consequences.** `source_register` is whole-KB write scope (dedup must see every
source), so a narrowed token writes pages with `concept_write` and cannot register;
`source_list` is a collection read filtered by visibility. A `provenance` entry
that equals an existing Source ID is a citation; anything else stays a plain note.
The audit trail records `map` (register) and `scope` (list) only: title, locator
and body are free text. `source_uncited` is per-concept and suppressible.
