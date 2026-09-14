---
topic: data-plane
---

# D106 — Concept-owned non-Markdown assets

**Decision.** An expanded concept may own regular non-Markdown files through the `asset_*` MCP family. Assets remain filesystem-and-git-history data: they carry no frontmatter or ConceptID and stay outside `WalkConcepts`, validation, search, and the concept graph. They follow an expanded owner's directory move; deleting an owner with assets requires explicit force.

**Rationale.** Expanded concepts already establish a stable owner directory without changing their ConceptID. Keeping attached evidence in that directory lets git preserve it across moves while the concept graph remains exclusively Markdown-to-Markdown, avoiding false search and lint results from binary or generated files.

**Alternative rejected.** Widening the provisioning `artifact_*` whitelist to `data/**` was rejected: provisioning artifacts and concept evidence have different validation, ownership, lifecycle, visibility, and `allow_artifact_write` semantics. A separate data-plane API preserves both boundaries.
