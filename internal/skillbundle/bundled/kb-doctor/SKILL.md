---
name: kb-doctor
description: Keep a Knowledge Base from rotting - a short, budgeted session that applies mechanical repairs and walks the operator through the server's ranked review list. Use when a tool result proposes a kb-doctor session, when kb_status reports conformance.doctor_suggested, when the operator asks to tidy or align a KB, or after a Cartographer upgrade.
version: "2.1"
---
# KB Doctor - Skill

## Purpose

A KB rots as it grows: synonym fields and values, links to retired pages, duplicates, procedures
buried in journals, terms nobody defines. The server finds and ranks it (`lint`, `kb_review`); this
session decides it with the operator. It is short on purpose: at most **10 decisions** per session
(the operator may ask for more), so it actually happens, and the KB converges over weeks.

Three trust levels, never mixed: **mechanical** fixes (`kb_repair`) are deterministic; a
**proposal** (a vocabulary) needs one approval; **judgement** (merge, move, close, promote) needs the
operator's choice per item. The server proposes a session when the KB's `doctor_interval` has passed
since the last one; it never runs one by itself.

## Procedure

1. **Signal.** `kb_status`: read `conformance` (`findings`, `fixable`, `last_doctor`,
   `next_doctor`, `doctor_suggested`), `review` (`total`, `by_kind`), `open_markers`, `read_cost`,
   and `capabilities.auto_repair`. Stop if nothing is suggested and the operator did not ask. Tell the
   operator in two lines what the session will cover.
2. **Mechanical.** For each check with a fix (`nonstandard_field`, `tool_param_field`, `broken_link`,
   `duplicate_link`, `invalid_field_value`, `prose_value`): `kb_repair` with `dry_run: true`.
   Checks listed in `capabilities.auto_repair.checks` the operator already trusts: apply them
   (`dry_run: false`) and report the counts. Any other: show the plan, apply on confirmation. One
   call is one commit; a concept in `skipped` changed since it was listed, so run the check again.
   `reciprocal_link_item` is not drift: propose it once per KB as an opt-in, with its count and the
   trade-off (backlinks keep the edge navigable; a reader of the raw file loses the reverse link).
3. **Cost (once per KB).** For each map whose index the operator keeps by hand (`index_incomplete`
   findings, or a long `index.md` rewritten in many sessions) propose `map_update` with
   `index: generated`: the server then keeps the concept list in a marked block, curated text
   outside it stays theirs. One decision per map; skip maps already generated.
4. **Vocabulary proposals.** A `missing_value_contract` finding carries a `proposal` (values and the
   synonyms it maps). One numbered decision per map; after approval `map_update` with those
   `field_values`, then `kb_repair invalid_field_value` and `kb_repair prose_value`.
5. **Review items.** `kb_review` with `limit: 10` (the budget, minus the decisions steps 3-4 used). Present
   **one numbered list**; for each item the kind, the concepts, the evidence and 2-3 options:
   - **act** with the ordinary tools: `concept_merge` or a cross-link (`duplicate_candidate`), close
     or update (`zombie_work`, `stale_open`, `closed_with_open_items`), `concept_new` from the target
     map's template and links both ways (`promotion_candidate`), a glossary entry
     (`glossary_gap`), `concept_move` (`map_misfit`), `concept_expand` or a split
     (`concept_oversize`); for `repeated_fact` choose the owner concept with the operator, keep the
     fact there and replace each copy with a link (never rewrite the fact); for `read_hotspot`
     `concept_expand` into satellites with a short summary page, or turn it into an index page;
   - **dismiss** with a reason: `concept_patch` adding the kind to `lint_ignore` on a concept the
     item names, `reason` saying why, so the history keeps it;
   - **defer**: nothing is written; it comes back next session.
   Never invent content: what the KB does not know becomes a `contradiction_report` of kind
   `open_question`. Run `gate_check` scoped to each map you changed.
6. **Artifacts.** `artifact_read` the KB's `instructions.md` and `templates/`: update any rule or
   template this session made obsolete (a field renamed, a workaround a repair removed, a rule to add
   pages to a now-generated index or to add reverse links).
7. **Close.** `log_append` whose text contains `kb-doctor` and the before/after counts per bucket,
   for example `kb-doctor: warnings 205 -> 0, fixable 40 -> 0, review 34 -> 25, open_markers 161 ->
   158`. `kb_status` reads `last_doctor` from it, which also stops the proposal for one interval.

## Rules

- Judgement is never applied without the operator's choice; a mechanical repair outside
  `auto_repair` never without a dry-run plan they saw.
- One numbered list per session; a deferred item is not asked again in the same session.
- Never overwrite by hand a concept `kb_repair` skipped: re-read it and run the check again.
- Do not invent contract values, glossary definitions or procedure steps.
