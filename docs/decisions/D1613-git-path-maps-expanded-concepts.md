---
topic: control-plane
---

# D1613 — `GitPathToConceptID` maps an expanded concept's `index.md`

**Decision.** `kb.GitPathToConceptID` maps `data/<map>/<concept>/index.md`, the body of an expanded concept (D77), to the concept ID `<map>/<concept>`, for every caller. A map's own `index.md`, the root `index.md`, `log.md`, `_map.md` and `_archive.md` stay unmapped. `changes_since` therefore reports an expanded concept's edits as concept changes and no longer counts them in `other_changes`.

**Why.** The function rejected every `index.md` as reserved, so `changes_since` (and the Atlas changes API, which reuses it) lost the creates, edits and deletes of expanded concepts, and a restricted token saw nothing of them. `linkSourceFromGitPath` already handled the case by itself, so the two mappings had drifted.

**Alternatives rejected.**
- A separate `conceptIDFromGitPath` used only by `changes_since`: a smaller blast radius, but two mappings that can drift again.

**Consequences.** The callers that already handled the expanded form on their own (conflict registration in `gitwrap.go`, which checks `expandedID` as well, and `IsReservedPath`) behave as before. The births map of the Atlas home now includes expanded concepts. A new path-to-concept mapping must go through this function.
