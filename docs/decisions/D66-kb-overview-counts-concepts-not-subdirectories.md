---
topic: control-plane
---

# D66 — `kb_overview` counts concepts, not subdirectories

**Context.** `toolKBOverview` printed for each archive `DossierCount`, which via `ListDossiers`
counts **subdirectories**. A flat-concept KB (e.g. `homelab-wiki`: `entities/x.md`, not
`entities/x/`) has 0 subdirectories → `0 dossiers` for each archive, and an agent reads it as
"empty KB" despite having dozens of healthy concepts (observed with Codex on 2026-07-05: 62 concepts,
`search`/`concept_read` ok, overview at `0 dossiers`).

**Decision.** New `KB.ConceptCount(archive)` recursively counts non-reserved `.md` files
(`WalkDir` + `okf.IsReserved`, excludes `index.md`/`log.md`/`_archive.md`/`AGENTS.md`).
`kb_overview` shows `(N concepts)` per archive, and `(N concepts, M dossiers)` when
subdirectories also exist — so flat KBs and dossier-based ones both stay readable. Requires
server redeploy.
