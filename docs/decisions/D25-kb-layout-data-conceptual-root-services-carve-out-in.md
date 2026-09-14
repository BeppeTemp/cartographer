---
topic: data-plane
---

# D25 — KB layout: `data/` conceptual root, `services/` carve-out in `ResolvePath`

**Decision.** The conceptual root is `kb.Root/data/` (`DataRoot()`); `ResolvePath` makes an
explicit carve-out for `services/*.md` (base = `kb.Root`), and `WalkConcepts` also walks
`services/` so that search/graph/lint/index see the Services.
**Rationale.** Without the carve-out, Services would be listed by `WalkConcepts` but
unreachable for reading via `ResolvePath` — a silent bug; the carve-out stays minimal
(one check on the first path segment).
Details: `docs/data-plane.md` §Filesystem layout of a KB.
