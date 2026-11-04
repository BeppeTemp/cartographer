---
topic: control-plane
---

# D1613 — `changes_since` maps an expanded concept's `index.md` to its concept

**Decision.** `changes_since` maps `data/<map>/<concept>/index.md`, the body of an expanded concept (D77), to the concept ID `<map>/<concept>` (`conceptIDFromGitPath`, which adds `kb.GitPathToExpandedConceptID` to `kb.GitPathToConceptID`), for a changed path and for the old path of a rename. Its edits are concept changes and no longer count in `other_changes`. A map's own `index.md`, the root `index.md`, `log.md`, `_map.md` and `_archive.md` stay unmapped. `kb.GitPathToConceptID` itself is unchanged.

**Why.** `changes_since` (and the Atlas changes API, which reuses it) lost the creates, edits and deletes of expanded concepts, and a restricted token saw nothing of them. `linkSourceFromGitPath` already handled the case by itself.

**Alternatives rejected.**
- Making `kb.GitPathToConceptID` expansion-aware for every caller: it also feeds the Atlas births map, and giving an expanded concept a birth breaks the 3D growth replay (the `graph3d` e2e suite fails, the view crashes). The birth of an expanded concept is a separate gap, not fixed here.

**Consequences.** Conflict registration keeps its own `expandedID` handling. A new path-to-concept mapping for history digests should go through `conceptIDFromGitPath`; the births feed stays on the narrower function until the growth replay accepts expanded concepts.
