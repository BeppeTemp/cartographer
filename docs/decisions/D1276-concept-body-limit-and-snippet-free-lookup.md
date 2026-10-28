---
topic: control-plane
---

# D1276 — A concept body is capped at 1 MiB, and the write-time fact lookup asks the index for ids only

**Decision.** Every concept write (`concept_write`, `concept_new`, `concept_patch`, `concept_batch`, move and transfer) refuses a body over `kb.MaxConceptBodyBytes` (1 MiB) with `body_too_large`, naming the size and the limit, before anything is written; `kb_repair` rewrites are exempt. The write-time `repeated_fact` lookup (D351) uses `sqlindex.SearchFTSIDs`, the same ranked hits without a `snippet()` excerpt.

**Why.** A 76 KB rewrite took 8 s and 200 KB took 108 s, with the KB write lock held: the lookup asked the trigram FTS5 index for excerpts it never read, and `snippet()` is quadratic in the matches inside one body (the page just written matches its own lines). Dropping the excerpt makes the lookup linear; the cap bounds what every other write-time check does on one body. The price: a page already over 1 MiB can no longer be edited through the tools, even by a frontmatter-only change, only by `kb_repair`.

**Alternatives rejected.**
- Only the cap, keeping the excerpt: a page at 1 MiB would still take minutes.
- A cap that exempts edits of an existing oversize page: it lets a write-time check run on a body the limit exists to keep out; such a page must be split.
- Making `snippet()` cheaper (fewer tokens): its cost is in the match count, and the lookup does not need an excerpt at all.

**Consequences.** `SearchFTSIDs` must keep returning the hits of `SearchFTSFiltered` in the same order; a caller that shows an excerpt keeps using the latter. Any new write-time lookup on the index asks for ids only unless it renders them. The limit is listed in `docs/control-plane.md` §Enforced limits.
