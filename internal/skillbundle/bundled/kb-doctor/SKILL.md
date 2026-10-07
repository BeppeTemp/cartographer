---
name: kb-doctor
description: Keep a Knowledge Base from rotting - a short, budgeted session that applies mechanical repairs and walks the operator through the server's ranked review list. Use when a tool result proposes a kb-doctor session, when kb_status reports conformance.doctor_suggested, when the operator asks to tidy or align a KB, or after a Cartographer upgrade.
version: "2.10"
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
since the last one; it never runs one by itself. What it does run by itself, once per
`doctor_auto_interval` (default daily), is the **background repair** (D323): the KB's `auto_repair`
checks, at most 50 concepts per check, one commit each with reason "auto-repair (background)". It is
not a session: it writes no `kb-doctor` log entry and cannot make a judgement.

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

1. **Signal.** `kb_status`: read `conformance` (`findings`, `fixable`, `repairable`, `last_doctor`,
   `next_doctor`, `doctor_suggested`), `review` (`total`, `by_kind`), `open_markers`, `read_cost`,
   and `capabilities.auto_repair`. Stop if nothing is suggested and the operator did not ask. Read
   the questions earlier sessions deferred: `contradiction_report` with `kind: "open_question"`
   (status open). Answer the ones the KB or the operator can now settle: `concept_patch` the answer
   into the question and set `resolution_status: resolved`. Tell the operator in two lines what the
   session will cover.
2. **Mechanical.** For each check in `conformance.repairable` (every check with a fixable finding,
   drift or not; `fixable` counts only the conformance ones): `kb_repair` with `dry_run: true`. Checks listed in `capabilities.auto_repair.checks` the operator already trusts:
   apply them (`dry_run: false`) and report the counts. While `capabilities.auto_repair.default` is
   true (the operator never wrote the list) it is `nonstandard_field`, `tool_param_field`,
   `invalid_field_value`, `duplicate_link` and `prose_value`: deterministic, never rewrite body text or
   drop a link (D323). The server applies them by itself every `doctor_auto_interval`, so they are
   usually already done: run them again only when `capabilities.doctor_auto_interval` is disabled or
   findings remain after the last write (a `skipped` entry, or a KB with more than 50 per check). To
   undo a repair commit, `repair_revert` with its `sha` (only `kb_repair` and auto-repair commits are
   accepted; the Atlas Maintenance panel lists them with the exact command). Any other: show the plan (`found_total` is the whole
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
     (`concept_oversize`); a `lint_judgement` item is a `lint` finding of that check, so decide it
     from `lint` (the check is the actionable unit) and do not count it twice; for `repeated_fact` choose the owner concept with the operator, keep the
     fact there and replace each copy with a link (never rewrite the fact); for `read_hotspot`
     `concept_expand` into satellites with a short summary page, or turn it into an index page;
     for `scattered_work` create a concept in the contract's `work_map` from its template (one per
     independent item, or one for the checklist), link both ways and replace the items with the link;
     for `map_naming` propose one scheme for every map title the evidence lists (one language,
     one shape, one capitalisation, recognisable from the folder), and after the operator agrees
     rename each with `map_update` `title` — never a map's folder, which would break every link;
     a subtitle the new title drops ("Incidents — dated post-mortems") becomes the first line of
     the map's `index.md` (`index_patch`) when the index does not already say it;
     for `status_reclassify` apply the proposed status with `concept_patch` `frontmatter:
     {status: "<proposed>"}`, reason "reclassify active to <proposed> (D321)"; when the proposal
     is `unknown`, ask the operator — or, if delegated, set `waiting_on: "operator decision"` and
     `review_after` 14 days from now and move on;
   - **dismiss** with a reason: `concept_patch` adding the kind to `lint_ignore` on a concept the
     item names, `reason` saying why, so the history keeps it (a `map_naming` item names maps as
     `<map>/_map`: ask the operator to add `lint_ignore: [map_naming]` to that map's `_map.md`; a
     `zombie_work` item whose first concept is retired is a shared origin — when it is where the
     others came from, dismiss it once on that retired concept);
   - **defer**: nothing is written and it comes back next session — unless the finding is on open
     work only the operator or a third party can unblock: then `concept_patch` `waiting_on` (who or
     what) and `review_after` (a date), which suspends `stale_open` until then and makes the wait
     visible (D321). Never touch `timestamp` to restart the clock.
   Never invent content: what the KB does not know becomes a `contradiction_report` of kind
   `open_question`: `title` is the question in one sentence, the body the evidence and the options,
   `involves` the concepts it concerns. It stays open across sessions, the Atlas Maintenance panel
   lists it for the operator, and the next session's step 1 reads it back. Run `gate_check` with `changed_ids` set to the concepts you wrote.
   Write responses surface per-concept findings inline; the `gate_check` at session end is the
   complementary pass.
5b. **Harvest and archive.** `kb_review` `kind: "harvest_candidate"`: a journal entry that is closed
   and older than the journal's `harvest_after` (default 45 days). For each:
   - `concept_read` it and pick the durable facts: root causes, recurring traps, diagnostic commands
     that worked, workarounds - what a future reader of the live subject page would need (the
     evidence lists the sections that look durable);
   - for each outbound link to a live concept (a map, not a journal) the fact belongs to, add it as
     a line under an existing section, or under `## Lessons from incidents` (or the KB's own heading
     for this), with a dated link back: `- <fact> ([YYYY-MM-DD - title](journal/entry))`;
   - `concept_patch` the entry to `status: archived` and add at the top of its body
     `> Archived YYYY-MM-DD: lessons in [page](id), [page](id).` (or `> Archived YYYY-MM-DD: no
     durable lessons.`);
   - create or update the quarterly digest `<journal>/archive-<year>-q<quarter>` (type `digest` when
     the map allows it - `map_update` `concept_types` adds it to a strict one - otherwise the
     journal's first allowed type, status `reference`) with one line per archived entry:
     `| entry title | outcome | lessons in |`;
   - `gate_check` the changed concepts.
   An entry with no durable facts is still archived, with "no durable lessons", and the digest
   records its outcome. Delegated (D305): extract the facts yourself and report what you wrote;
   attended: show the candidate and the proposed lessons for the operator to confirm. Archived
   entries leave default `search`, `read_cost` and the atlas structure; `search` with
   `include_archived: true` still finds them.
6. **Advice.** `lint` with `severity_min: info`: an `info` finding is advice the KB has not
   answered yet, and the session is done only when nothing is left — fixed, or accepted where the
   KB says so. Fix what is a defect (`orphan`: link the page with `link_suggest`; `bare_link_list`:
   one reason per link; `link_to_retired` in a live page: update the sentence or point at the
   successor). Accept what is a choice with `lint_ignore` and a `reason`: on the concept for a
   one-off, map-wide with `map_update` `lint_ignore` (the list replaces the map's: extend the
   `lint_ignore` that `map_list` shows) when the whole map follows that style. An artifact finding
   (a skill, an agent: `acceptability` says `artifact`) is accepted in `instructions.md`
   `lint_accept`, keyed by the path the finding names, with `artifact_write`. Each accepted check
   counts against the budget once per map, not once per concept. Many per-concept writes go in
   one `concept_batch` (up to 50 operations, one commit), not one call each.
   - **Page names** (D315): `title_h1_mismatch` is mechanical (`kb_repair`, the heading follows the
     title); `title_quality` is an info finding with no fix, because the wording is a judgement:
     show the operator the title and a shorter label, and accept it with `lint_ignore` when the
     title is deliberate.
   - **Boilerplate** (D314, D317): a `repeated_fact` whose evidence line comes from a template is
     structural. The fix is in the template (`artifact_read` `templates/`), not in each concept:
     one decision, not one per copy.
   - **Search misses**: `kb_status` `search_misses`. A miss with `resolved: true` found something
     on the last check and needs nothing (D319); a still-open one, asked more than once, is a gap:
     write the page if the KB has the facts, otherwise an `open_question`.
7. **Artifacts.** `artifact_read` the KB's `instructions.md` and `templates/`: update any rule or
   template this session made obsolete (a field renamed, a workaround a repair removed, a rule to add
   pages to a now-generated index or to add reverse links).
8. **Close.** `log_append` whose text contains `kb-doctor` and the before/after counts per bucket,
   for example `kb-doctor: warnings 205 -> 0, fixable 40 -> 0, review 34 -> 25, open_markers 161 ->
   158`. `kb_status` reads `last_doctor` from it, which also stops the proposal for one interval.
9. **Volume.** A KB imported with more than 200 findings is not a 10-decision session; work it in
   this order, over several sessions if needed. First the mechanical repairs that change the graph
   (link forms, frontmatter normalisation): many findings disappear after them, so re-run `kb_status`
   to recount before planning anything else. Then delegate the body-text checks (`bare_link_list`,
   `link_to_retired`) in `concept_batch` calls of up to 50 operations, one commit each, to up to 4
   subagents working on disjoint maps. Judgement items (`kb_review`) come last, from the heaviest.

## Rules

- Judgement is never applied without the operator's choice, and a mechanical repair outside
  `auto_repair` never without a dry-run plan they saw — unless the session is delegated (see
  Purpose), and then every choice is in the closing report.
- One numbered list per session; a deferred item is not asked again in the same session.
- Never overwrite by hand a concept `kb_repair` skipped as changed: re-read it and run the check
  again. A fix skipped as needing a person is written by hand, after reading the concept.
- Never delete a journal entry during archival, and never move it out of its journal.
- The quarterly digest is created if it does not exist and updated if it does; its status is
  `reference`, never `archived`.
- Do not invent contract values, glossary definitions or procedure steps.
