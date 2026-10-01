---
topic: data-plane
---

# D297 — Lint sees a KB decay

**Decision.** Five `info` checks report decay: `stale_open` (an open status older than the map's `stale_after`), `closed_with_open_items` (a done/resolved status with unchecked items), `template_section_missing` (opt-in per map: the page lacks H2 sections of `templates/<type>.md`), `open_marker` (the KB's own open-question words, counted in `kb_status.open_markers`) and `facet_sprawl` (a `tags` facet with ≥ 30 values, half used once). Four contract keys tune them: `open_statuses`, `stale_after`, `template_sections`, `open_markers`, settable through `map_update`. None has a fix: they are the doctor's input. `stale_open` and `closed_with_open_items` count in `kb_status.conformance`.

**Why.** Lint saw the graph and the frontmatter contract, not a KB rotting. On a ~740-concept work KB: 32 open-like concepts untouched past the journal default, 18 closed ones with open checklists, 17/17 pages of one type and 1 of another missing sections of their template, 161 open-question markers in 73 pages once the map declared its own words, and six maps whose tags were mostly singletons (one with 332 values, 204 used once). Nothing counted any of it.

**Alternatives rejected.** Warnings: a KB with long-lived open work would go red on upgrade for something that is a choice. Template sections always on: many KBs use templates as guidance; opt-in keeps them quiet until a map promises a shape. A built-in multilingual marker list: the words are the KB's; the default is the three universal ones, and a map names its own. `active` as open everywhere: in a reference map it means "valid", so only journals treat it as unfinished. Fixes: closing, filling or retagging is judgement.

**Consequences.** The template slug is the type lowercased. A template written as a fenced markdown sample contributes the H2s of its first fenced block, and an opener with an info string inside an open block starts a new sample (the measured template had two samples and an unclosed fence). `MapContract` gains `Kind`, `OpenStatuses`, `StaleAfterDays`, `TemplateSections`, `OpenMarkers`; `Finding` gains `Count`. Room for the `map_update` keys in the agent tool budget (D285) came from dropping per-property descriptions in `map_create` and shortening two tool descriptions. The `kb-doctor` skill (1.3) walks the new checks without inventing answers: an unknown becomes an `open_question` gap (D273).
