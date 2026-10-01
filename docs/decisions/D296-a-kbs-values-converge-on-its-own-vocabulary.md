---
topic: data-plane
---

# D296 — A KB's values converge on its own vocabulary

**Decision.** Lint knows families of `status` synonyms (`lint.ValueSynonymFamilies`) and a map may extend them with `value_synonyms.<canonical>: [...]`. The canonical member is always the KB's: the value its contract declares, or, when proposing a contract, the family's most frequent observed member. `missing_value_contract` on `status` carries a structured `proposal` (values plus a synonym and prose mapping). `invalid_field_value` carries `set_value` when the value is exactly one allowed value up to folding or by family. A new `prose_value` warning reports a sentence in a vocabulary field and carries `split_value`, which keeps the token in the field and moves the rest into the body. `kb_repair` applies both.

**Why.** D289 converged field names, but on five real KBs the larger rot was in values. A ~740-concept work KB had 16 distinct `status` values (`done` next to `completato`, `in-progress` next to `in-corso`, incidents `resolved` next to `closed`) and no contract, so `concept_list(where: status=done)` silently missed synonyms. Another KB used `status` as prose ("accepted — implemented in commit …"), so every such concept was a facet value of one. A third renamed `stato` → `status` (D289) and inherited Italian values as new synonyms. One KB had a deliberate backlog vocabulary (`open`, `decision-needed`, `done`) that a product-default normalisation would have destroyed.

On copies of those KBs the proposals came out as the KBs' own vocabularies with the synonyms mapped, and after declaring one ADR map's proposal with `map_update`, `kb_repair prose_value` converged its two prose statuses in one commit, prose kept in the body.

**Alternatives rejected.** A product vocabulary every KB is normalised to: it flattens KB-specific states. Per-concept fixes without a contract: deciding the vocabulary is a judgement, the same stance as D289. Dropping the prose: it is often the only record of a condition or a commit. An English-only family table: the drift measured was mostly in another language; extensible families with a declared contract escape hatch give an answer where one is known and none where it is not.

**Consequences.** `ValueSynonymFamilies` is documented row by row in `docs/data-plane.md`; a test fails if a value sits in two families or collides with a `StandardFieldSynonyms` key. `lint.FixableChecks` gains `invalid_field_value` and `prose_value`; `kb_status.conformance` counts both. `Finding` gains `Proposal`, returned as `proposal`. `value_synonyms` uses dotted keys like `field_values.*` (the frontmatter parser has no nested maps), not the nested form the plan sketched. A prose head of more than three words proposes its first word, so a value that starts with a long phrase may need an operator's correction of the proposal.
