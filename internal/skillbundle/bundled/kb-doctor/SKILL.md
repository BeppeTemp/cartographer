---
name: kb-doctor
description: Keep a Knowledge Base from rotting - a short, budgeted session that applies mechanical repairs and walks the operator through the server's ranked review list. Use when a tool result proposes a kb-doctor session, when kb_status reports conformance.doctor_suggested, when the operator asks to tidy or align a KB, or after a Cartographer upgrade.
version: "2.6"
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

**Delegation.** The operator may hand the session over ("do it yourself"), or the KB's
`instructions.md` may say doctor sessions run unattended. Then you decide every item yourself,
with the option you would have recommended, and report afterwards instead of asking first:
mechanical repairs after their dry run, proposals, judgement. Delegation changes who chooses, not
what is allowed:
- decide from the KB, not from guesses: before closing work whose subject looks retired, `search`
  for what replaced it; when the KB cannot tell (a decision only the operator can make, a fact it
  does not hold), **defer** the item — dismiss only the signal, never the open question;
- never delete a concept, never rename a map's folder, never invent content;
- every write carries a `reason` saying why, so the history explains the session;
- end with one list: each decision, what was written, and what was deferred and why.

## Procedure

1. **Signal.** `kb_status`: read `conformance` (`findings`, `fixable`, `last_doctor`,
   `next_doctor`, `doctor_suggested`), `review` (`total`, `by_kind`), `open_markers`, `read_cost`,
   and `capabilities.auto_repair`. Stop if nothing is suggested and the operator did not ask. Tell the
   operator in two lines what the session will cover.
2. **Mechanical.** For each check with a fix (`nonstandard_field`, `tool_param_field`, `broken_link`,
   `duplicate_link`, `invalid_field_value`, `prose_value`): `kb_repair` with `dry_run: true`.
   Checks listed in `capabilities.auto_repair.checks` the operator already trusts: apply them
   (`dry_run: false`) and report the counts. Any other: show the plan (`found_total` is the whole
   job, `planned_total` what this call covers under `limit`), apply on confirmation. One call is
   one commit. A `skipped` entry is either a concept changed since it was listed (run the check
   again) or a fix that needs a person — two synonyms of one field holding different values: pick
   the value with the operator and write it with `concept_patch`; the concept's other fixes were
   already applied.
   `reciprocal_link_item` is not drift: propose it once per KB as an opt-in, with its count and the
   trade-off (backlinks keep the edge navigable; a reader of the raw file loses the reverse link).
   A mutual pair whose only back-link is in the target's own links section is not flagged, and
   the repair never drops both sides of a pair (D309). A server older than that fix flagged both
   sides of such a pair and the repair dropped the edge: there, run it with `dry_run: true` first
   and leave out any concept that appears on both sides of a pair.
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
     for `scattered_work` create a concept in the contract's `work_map` from its template (one per
     independent item, or one for the checklist), link both ways and replace the items with the link;
     for `map_naming` propose one scheme for every map title the evidence lists (one language,
     one shape, one capitalisation, recognisable from the folder), and after the operator agrees
     rename each with `map_update` `title` — never a map's folder, which would break every link;
     a subtitle the new title drops ("Incidents — dated post-mortems") becomes the first line of
     the map's `index.md` (`index_patch`) when the index does not already say it;
   - **dismiss** with a reason: `concept_patch` adding the kind to `lint_ignore` on a concept the
     item names, `reason` saying why, so the history keeps it (a `map_naming` item names maps as
     `<map>/_map`: ask the operator to add `lint_ignore: [map_naming]` to that map's `_map.md`; a
     `zombie_work` item whose first concept is retired is a shared origin — when it is where the
     others came from, dismiss it once on that retired concept);
   - **defer**: nothing is written; it comes back next session.
   Never invent content: what the KB does not know becomes a `contradiction_report` of kind
   `open_question`. Run `gate_check` with `changed_ids` set to the concepts you wrote.
   Write responses surface per-concept findings inline; the `gate_check` at session end is the
   complementary pass.
6. **Advice.** `lint` with `severity_min: info`: an `info` finding is advice the KB has not
   answered yet, and the session is done only when nothing is left — fixed, or accepted where the
   KB says so. Fix what is a defect (`orphan`: link the page with `link_suggest`; `bare_link_list`:
   one reason per link; `link_to_retired` in a live page: update the sentence or point at the
   successor). Accept what is a choice with `lint_ignore` and a `reason`: on the concept for a
   one-off, map-wide with `map_update` `lint_ignore` (the list replaces the map's: extend the
   `lint_ignore` that `map_list` shows) when the whole map follows that style. Each accepted check
   counts against the budget once per map, not once per concept. Many per-concept writes go in
   one `concept_batch` (up to 50 operations, one commit), not one call each.
7. **Artifacts.** `artifact_read` the KB's `instructions.md` and `templates/`: update any rule or
   template this session made obsolete (a field renamed, a workaround a repair removed, a rule to add
   pages to a now-generated index or to add reverse links).
8. **Close.** `log_append` whose text contains `kb-doctor` and the before/after counts per bucket,
   for example `kb-doctor: warnings 205 -> 0, fixable 40 -> 0, review 34 -> 25, open_markers 161 ->
   158`. `kb_status` reads `last_doctor` from it, which also stops the proposal for one interval.

## Rules

- Judgement is never applied without the operator's choice, and a mechanical repair outside
  `auto_repair` never without a dry-run plan they saw — unless the session is delegated (see
  Purpose), and then every choice is in the closing report.
- One numbered list per session; a deferred item is not asked again in the same session.
- Never overwrite by hand a concept `kb_repair` skipped as changed: re-read it and run the check
  again. A fix skipped as needing a person is written by hand, after reading the concept.
- Do not invent contract values, glossary definitions or procedure steps.
