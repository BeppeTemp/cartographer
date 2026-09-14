---
topic: data-plane
---

# D77 — Atlas/Map/Journal hierarchy: the dossier becomes a state of the concept

**Status: implemented (2026-07-10). `homelab-wiki` migration executed and verified (2026-07-10).**

**Context.** Analysis of the `homelab-wiki` KB (66 concepts): the "dossier" level was used exclusively as a taxonomic category (`entities/{ai-tools,infra,smart-home,clients}`, `topics/{...}`) — zero authentic dossiers in the model's sense ("coherent unit on a topic"). Three causes: (1) archives defined by ontological type (`entities`/`topics`) that duplicate the `type` frontmatter and leave thematic categories homeless; (2) dossier defined only structurally (any subdir), no semantic invariant; (3) file→folder promotion left to the agent (rename+backlinks), hence never performed — and with the 3-segment depth cap, category-dossiers made the true dossier *impossible*.

**Decision.**

a) **Lexicon** (agent-facing surface, English — open project): **Atlas** = the KB; **Map** = thematic archive with mixed `concept_types` (Entity and Topic of the same domain coexist: the type is an attribute, not a position); **Journal** = chronological append-oriented registry archive (incidents, notes). The "dossier" disappears as a noun: it is a **state** of the concept — *expanded concept*, directory `map/nome/` with `index.md` and satellites.

b) **Descriptor** `_map.md` (`type: Map`, `kind: map|journal`, default map; `archive_type` retired). Legacy `_archive.md` stays read-compat (treated as a Map with `kind: map`, never written again, `legacy_archive_descriptor` lint).

c) **ID resolution with fallback**: `map/nome` resolves to `map/nome.md` or, if absent, to `map/nome/index.md` — on the read *and* write paths. Thus `concept_expand(id)` (new tool) promotes a concept to an expanded concept **without changing the ConceptID and without backlink rewrite** (unlike `concept_move`). Both forms present = write error (`expanded_ambiguous`, also a lint check with error severity). `WalkConcepts` emits `map/concept/index.md` as concept `map/concept` (search/graph/lint see it). No inverse (`concept_collapse`): YAGNI. Max depth unchanged (3 segments, `map/concept/child`). Expansion also allowed in journals (heavy incident with attachments).

d) **MCP surface v2 (breaking, → release v2.0.0)**: `kb_overview`→`atlas_overview`, `archive_list`→`map_list` (exposes `kind`), `archive_create`→`map_create(kind)`; `dossier_list`/`dossier_create` **removed with no alias** (inventory: `concept_list`/`index_get`; growth: `concept_expand`). CLI `import`: `--archive`→`--default-map` (the `--map` name was already taken by the per-directory mapping — a deliberate deviation from the plan, which called for `--map`).

e) **Deterministic lint guardrails (never LLM)**: category navigation is the job of curated indexes/search/graph, not the filesystem — `expanded_as_category` (>8 children mostly not linked to the concept's index), `map_oversize` (>50 concepts: thematic split, not subfolders; `info` severity, new), `expanded_missing_index` (rename of `dossier_missing_index`), `expanded_ambiguous`, `legacy_archive_descriptor`. Thresholds as named constants in `internal/lint/lint.go`.

f) **Rename scope**: MCP surface + data model + docs only. The `internal/kb` package, the `--kb` CLI flags, the `kbs:` config and the `CARTOGRAPHER_*` envs are **not** renamed (possible future D); the low-cost inconsistent internal identifiers (`ListDossiers`→`ListExpanded`, `DossierCount`→`ExpandedCount`) are.

**Rationale.** Every physical hierarchy is under pressure to become a taxonomy; the model must defend itself instead of relying on the agent's discipline. Making expansion an atomic, ID-preserving operation removes the cost that prevented authentic dossiers from being born; removing the noun removes the level that invited abuse; the lints flag the drift when it reappears. Existing KBs stay readable without migration (read-compat b/c): the `homelab-wiki` migration (merge `entities`+`topics` by theme → maps `smart-home`/`infra`/`ai-tools`/`clients`, `incidents`/`notes` journals) is operational, post-deploy, via batch `concept_move`.
