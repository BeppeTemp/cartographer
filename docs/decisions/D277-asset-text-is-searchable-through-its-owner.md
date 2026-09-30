---
topic: data-plane
---

# D277 — Asset text is searchable through its owner concept

**Decision.** `search` indexes the text of an expanded concept's text assets
into the owner's own search document, in a fourth field (`assets`, body weight
1 in memory and in bm25). A hit is the owner concept; there is no per-asset
result. Indexable: a listed text extension, not `oversized` (D270), valid
UTF-8 without NUL, first 256 KiB. Assets are found again by stat signature
(path, size, mtime), not by reading them: the SQLite hash of an owner with
indexable assets is `contentHash + "+" + sha256(signature)[:16]`, and a
concept without them keeps its plain hash.

**Why.** An asset is dossier evidence (D106), but a value that lived only in
one, such as a MAC in an inventory CSV or a flag in a config, was unfindable
unless the agent already knew which dossier to open. The owner is the unit of
retrieval everywhere else (graph, lint, visibility), so the owner's
visibility governs the hit with no new policy.

**Alternatives rejected.**
- *Per-asset hits:* FTS5 cannot cheaply say which column matched, and every
  consumer of a hit (`concept_read`, visibility, `graph_context` seeding)
  expects a concept id.
- *Content hashes of assets on every reconcile:* reads every asset on every
  `search`; a stat signature reads none.
- *Extracting PDF/DOCX/images:* needs a dependency (`docs/conventions.md`); the
  ingestion skill has the agent write their content into the concept.
- *Indexing assets as their own rows:* would make them concepts, which D106
  says they are not.

**Consequences.** The SQLite schema is version 3 (an `assets` column): the
index is rebuilt once on first start after the upgrade. The signature rule is
the graph cache's (D241, D245), including the racy window: a signature marked
racy is never equal to itself, so the owner is re-read after the window, and
the persisted hash never vouches for a racy row. The stat walk lists each
concept directory and stats each candidate file, so a no-change reconcile
costs a few milliseconds per thousand assets
(`BenchmarkReconcileNoChangeWithAssets`). The SQLite snippet is taken from the
assets column only when the body excerpt carries no match mark. A change to
the list of indexable extensions changes what `search` can find without any
file changing: reindex with `full: true`.
