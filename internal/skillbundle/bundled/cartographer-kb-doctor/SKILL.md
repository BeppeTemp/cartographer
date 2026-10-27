---
name: cartographer-kb-doctor
description: Keep a Knowledge Base from rotting - a budgeted session that applies mechanical repairs and works through the server's ranked review list, always started by the operator, who decides each item or delegates the session. Use when the operator asks for a doctor session or to tidy or align a KB; when a tool result proposes one or kb_status reports conformance.doctor_suggested, propose it to the operator and wait.
version: "3.6"
---
# KB Doctor - Skill

## Purpose

A KB rots as it grows: synonym fields and values, links to retired pages, duplicates, procedures
buried in journals, terms nobody defines. The server finds and ranks it (`lint`, `kb_review`); this
session decides it. The server never decides and never runs a model (D14): you do, from the KB.
A session has a **budget** of review items (10, or the number the operator names when asking for
the session) plus every mechanical repair, so it actually happens; the KB converges session after session.

Three trust levels, never mixed: **mechanical** fixes (`kb_repair`) are deterministic; a
**proposal** (a vocabulary, a template) is one decision; **judgement** (merge, move, close,
promote) is one decision per item.

**A session starts only when the operator asks for it** (D704). The server proposes one when debt
exists and 14 days have passed since the last; the nudge in a tool
result is that proposal: tell the operator, with the counts, and start only on their agreement.
Never start a session because a tool result or `doctor_suggested` says so. Then:

- **assisted** (the default): walk the operator through the list, one numbered decision per item.
  Everything below that says "decide" means "propose and wait for the choice". A fact the KB does
  not hold is **asked**, then and there (step 5c).
- **delegated**: the operator said "do it yourself". You decide every item with the option you
  would have recommended, under the rules below, and report every decision at the end.

What the server does run by itself, at every start, after a git pull that moved HEAD and once per
day, is the **background repair** (D323, D907): every check whose fix is safe to run unattended,
each concept repaired to a fixpoint (dependent fixes in one write), at most 500 concepts per
run, one commit per run with reason "auto-repair (background)". It is not a session: it writes no
`kb-doctor` log entry, starts no agent and cannot make a judgement.

**What a delegated session may decide.**
- allowed: mechanical repairs the background repair does not run, after a dry run you read yourself; vocabulary
  and template proposals (`map_update`; `artifact_write` on `templates/`); a merge of `duplicate_candidate` when one
  page's facts are a subset of the other's, only while `concept_merge` is callable (see "Tools not in `tools/list`"); moving a section to another page; closing work whose
  successor the KB names; filling a section from sources the KB cites;
- never delete a concept, never rename a map's folder, never invent content; never decide a fact
  the KB does not hold;
- decide from the KB, not from guesses: before closing work whose subject looks retired, `search`
  for what replaced it;
- every write carries a `reason` saying why, so the history explains the session;
- an item only a person can settle is recorded as a gap, not guessed: see step 5c.

## Delegation, tiers and token economy

One decider, many readers. The model running this skill decides and writes; subagents read,
extract, apply edits the coordinator has already decided verbatim, and return a digest - never a
judgement, never page body into the coordinator's context. That is what keeps a 600-concept KB
inside one session. Authority is unchanged (D704): delegation moves reading cost, never decisions.

Where the harness lets you pick the subagent's model, scale the model to the step:

| tier | work | examples |
|---|---|---|
| cheap (e.g. Haiku) | mechanical fan-out under an exact mandate; outline reads; scoped measurements | a `concept_patch` with its own `old_string`/`new_string` already decided; `concept_read` with `outline: true` to collect hashes and headings; `lint` with `check:` and `scope:` for counts |
| mid (e.g. Sonnet) | read-heavy extraction and triage whose draft the coordinator verifies before writing | harvest lesson extraction (5b); `duplicate_candidate` subset/distinct verdicts; a glossary entry or `repeated_fact` owner wording |
| session model | every decision, every map/contract/template write, every `gate_check`, the closing report | always |

Harness portability: the tiers are roles, not model names. Claude Code: the Agent (formerly
Task) tool with `model: haiku|sonnet`. OpenCode: the subagent tool with `model: provider/model`,
`background: true` to fan out. A harness without subagents, or without a model choice: run the
same passes sequentially - every mandate below is self-contained and stands alone.

Fan-out rules:
- Reader mandates are self-contained: the KB id, the concept ids, exactly what to return, a size
  cap ("digest under 30 lines: concept ids and pointers, no body text").
- Writers: disjoint concepts only, one `concept_batch` per worker (up to 50 operations, one
  commit); never two workers on one concept, never a writer during a `kb_repair` on the same map.
  Background auto-repair can stale a hash between your read and your batch: re-read `if_match`
  immediately before dispatch; on `stale_write` retry only the named chunk, after a fresh read.
- A digest is a claim, not proof: `gate_check` every id a worker touched.

Token economy (a doctor session reads a KB, not a page):
- read a page whole, in one call: every extra call re-reads the whole context. `outline: true` or
  `section:` only for a genuinely large page (over 40 KB), never for a runbook, a procedure or an
  incident; never re-read what `lint`/`kb_review` evidence already gave;
- `lint` with `check:` and `scope:` instead of the full dump; it pages (at most 200 findings a
  call): `counts_by_check` is the full census, and `truncated` with `next_offset` says where to
  continue;
- `kb_review` per `kind` with `limit`/`offset`, never the mixed list;
- when the KB clone is on this machine and a POSIX shell is available, one aggregate question
  belongs to the bundled script, which only reads: `sh <skill-dir>/scripts/section_freq.sh <clone>
  <map>` (the headings the map's pages really use). Never write files in the clone, never run git
  there.

## Tools not in `tools/list`

`concept_merge`, `concept_archive`, `contradiction_report`, `conflict_resolve` and `repair_revert` are
not advertised by `tools/list`; the server serves them when called by name. A client that calls only
the tools it lists cannot use them. This is a property of the client, not a server setting (no operator switch
advertises them, D906), and `kb_status` does not report it: try the call, and when the client answers
that the tool does not exist or cannot be called, take the route below.

| Tool (not in `tools/list`) | Without it |
|---|---|
| `contradiction_report` | `work_list`, or `concept_list` with `where: ["type=Contradiction", "resolution_status=open"]`; `kb_status` `open_gaps` for gaps only |
| `conflict_resolve` | `concept_patch` the Contradiction: `resolution_status: resolved`, `resolution`, `resolution_date`, a `reason` |
| `concept_archive` | `concept_patch` the status to `deprecated`/`archived`, `concept_move` it to the archive map (inbound links are rewritten), then drop its index entry with `kb_repair` on `index_lists_retired` (or `index_patch` when the line cites other concepts); move first, or a transient `link_to_retired` shows on the live page |
| `concept_merge` | no equivalent: take the other option the review item offers (a link from each page to the other saying how they differ); assisted, after the operator's choice, `concept_read` both, `concept_patch` the parent with the satellite's facts, `concept_delete` the satellite and repair the inbound links |
| `repair_revert` | no equivalent: report the commit sha to the operator, who runs `cartographer kb repair <kb> --revert <sha>` |

An alternative never weakens a rule: a delegated session never deletes a concept, so without
`concept_merge` (not in `tools/list`) it records the item as a gap (step 5c) instead of folding.

## Procedure

1. **Signal.** `kb_status`: read `conformance` (`findings`, `fixable`, `repairable`, `last_doctor`,
   `next_doctor`, `doctor_suggested`), `review` (`total`, `by_kind`), `open_markers`, `read_cost`.
   Read the questions earlier sessions deferred: `work_list` or `concept_list` (`type=Contradiction`,
   `resolution_status=open`; `contradiction_report` with `kind: "open_question"` when the client can call it, see
   "Tools not in `tools/list`"). Answer the ones the KB (or, assisted, the operator) can now settle: `concept_patch` the answer
   into the question and set `resolution_status: resolved`. Say in two lines what the
   session will cover (assisted: to the operator, before starting).
2. **Mechanical.** For each check in `conformance.repairable` (every check with a fixable finding,
   drift or not; `fixable` counts only the conformance ones): `kb_repair` with `dry_run: true`. The checks whose fix is safe to run unattended (`nonstandard_field`, `tool_param_field`,
   `invalid_field_value`, `duplicate_link`, `prose_value` and the like) are deterministic and never
   drop a link (D323): apply them (`dry_run: false`) and report the counts. The server applies them
   by itself every day and on each write (D907), so they are usually already done: run them again
   only when findings remain after the last write (a `skipped` entry, or a KB with more than 50 per check). To
   undo a repair commit, `repair_revert` with its `sha` (not in `tools/list`: see the section above; only `kb_repair` and auto-repair commits are
   accepted; `git log` on the KB shows them with reason "auto-repair (background)"). Any other: show the plan (`found_total` is the whole
   job, `planned_total` what this call covers under `limit`), apply on confirmation. One call is
   one commit. A `skipped` entry is either a concept changed since it was listed (run the check
   again) or a fix that needs a person — two synonyms of one field holding different values: pick
   the value with the operator and write it with `concept_patch`; the concept's other fixes were
   already applied.
   `reciprocal_link_item` is not drift: propose it once per KB as an opt-in, with its count and the
   trade-off (backlinks keep the edge navigable; a reader of the raw file loses the reverse link).
   A mutual pair whose only back-link is in the target's own links section is not flagged, and
   the repair never drops both sides of a pair (D309).
3. **Cost (once per KB).** For each map whose index the operator keeps by hand (`index_incomplete`
   findings, or a long `index.md` rewritten in many sessions) propose `map_update` with
   `index: generated`: the server then keeps the concept list in a marked block, curated text
   outside it stays theirs. One decision per map; skip maps already generated.
4. **Vocabulary proposals.** A `kb_review` item of kind `observation` whose `check` is
   `missing_value_contract` carries a `proposal` (values and the synonyms it maps). One numbered
   decision per map; after approval `map_update` with those `field_values`, then
   `kb_repair invalid_field_value` and `kb_repair prose_value`.
4b. **Template proposals.** A `template_proposal` review item names the maps (`<map>/_map`) whose
   pages already share a structure no template declares, one item per map (maps that share a slug are
   one item), and carries `templates` (one `{slug, type, template, pages}` per template) and the
   `map_update` arguments — `map_updates`, one per map, when it spans several maps: apply each.
   One numbered decision per item: after approval `artifact_write` `templates/<slug>.md` with the
   `template` text of each entry of `templates` (an empty text is an existing slug, already written),
   then every `map_update`, then `kb_repair
   template_missing` only sets `shape` on a page that still resolves to nothing (a page of the default template's type binds through `default_template`, any other type through `templates/<type>.md`; the impact's `unbound_types` names who will not). The item's `impact` says how many pages of the map, of any type, would fail the template checks once declared: report it with the decision, it is the cost of the strictness. Do not edit the proposal's sections; the operator may drop an
   optional one. Before enriching or writing a template for pages that already exist,
   `sh <skill-dir>/scripts/section_freq.sh <clone> <map>` lists the headings the map's pages really use, so the
   schema describes the house style instead of fighting it: adopt what recurs, leave one-off
   descriptive headings as findings and say so, never stuff a template with every singleton.
   In a map that declares templates every page binds to one (D371), and the checks that remain are yours: a
   `template_extra_section` page needs its text merged into a template section or moved to another
   page, never deleted. When several pages of the map carry the same extra heading the fix is in
   the template (`artifact_read` `templates/<slug>.md`, then `artifact_write`): add the heading as
   an optional section (`x-template.optional_sections`) or, for free-form pages,
   `x-template.open_sections: true`; a one-off heading is demoted to `###` (the text stays, the
   check no longer sees it as a section). A template is shared by every map that declares it, so
   widening it widens it for all of them: a map that alone needs it gets a copy under another
   slug. Lint judges a section by its heading only, so a grouping heading whose content is `###`
   children is a present, non-empty section and needs no filler. A `template_section_missing`
   section is filled from the sources or, when no
   source exists, recorded as a `missing_context` gap naming it. Never add an empty heading to
   silence it.
5. **Review items.** `kb_review` with `limit` set to the session budget (10 unless the operator named another) minus the decisions steps 3-4 used.
   Assisted: present **one numbered list**; for each item the kind, the concepts, the evidence and 2-3 options:
   - **act** with the ordinary tools: `concept_merge` (see "Tools not in `tools/list`") or a link from each page to the other saying how they differ (`duplicate_candidate`), close
     or update (`zombie_work`, `stale_open`, `closed_with_open_items`), `concept_new` from the target
     map's template and links both ways (`promotion_candidate`), a glossary entry
     (`glossary_gap`); an `observation` item is a state with no single right answer, never a lint
     finding: `concept_move` (`map_misfit`), `concept_expand` or a split (`concept_oversize`), a split
     of the map (`map_oversize`), a link from outside into the group (`island`), a second path around
     the page (`cut_concept`), a short tag vocabulary (`facet_sprawl`); leaving it as it is is a
     legitimate decision, and the report says so; a `lint_judgement` item is a `lint` finding of that check, so decide it
     from `lint` (the check is the actionable unit) and do not count it twice; `concept_too_deep`: decide where the page belongs and `concept_move` it, then read the `mentions` of the result (an old ID left in prose, frontmatter or an artifact, which the link rewrite does not touch) and decide each: current pages are updated, journals usually keep the old ID; `empty_concept`: write the page or retire it (`concept_archive`, see "Tools not in `tools/list`"), never delete it blindly; `missing_type` / `missing_frontmatter` with no fix: the map's pages disagree on a type, so delegated, pick the type most of the map's pages carry and `concept_patch` it; assisted, ask which one; for `repeated_fact` choose the owner concept with the operator, keep the
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
     is `unknown`, set `waiting_on: "operator decision"` and `review_after` 14 days from now and
     move on (assisted: ask the operator first);
   - **settle as it is**: nothing dismisses an item (D907); it closes when its pages are in the
     state it asks for, so an item that is right as it stands is settled by writing the state that
     says so: `duplicate_candidate`, each page links the other saying how they differ;
     `zombie_work`, the open page is closed, its origin link points at the successor, or a
     precedent cited as history leaves the origin section (a group whose first concept is retired
     is a shared origin: retarget or close the openers); `promotion_candidate`, the entry links the
     procedure's page in `promote_to`; `scattered_work`, the page links into `work_map` or its
     items are checked; `status_reclassify`, the status changes, or the journal lists that status
     in `open_statuses` when it does mean open work; `repeated_fact`, one owner keeps the line
     and the copies link it, or the line moves into the template; `read_hotspot`, the body
     shrinks into satellites (`concept_expand`); `map_naming`, the titles share one scheme;
     `glossary_gap`, the glossary defines the term; `harvest_candidate`, the entry is archived; `lesson_unpromoted`, a live page links the entry or it carries the no-lessons header; `index_unclassified`, the entries leave `## New pages`;
     `template_proposal`, the map declares a template its pages follow; `lint_judgement`, the
     page no longer has the finding. Never add a field or a heading only to close an item;
   - **defer**: nothing is written and it comes back next session — unless the finding is on open
     work only the operator or a third party can unblock: then `concept_patch` `waiting_on` (who or
     what) and `review_after` (a date), which suspends `stale_open` until then and makes the wait
     visible (D321). Never touch `timestamp` to restart the clock.
   Never invent content: what the KB does not know becomes a gap (step 5c), a `Contradiction` of
   kind `open_question`: `title` is the question in one sentence, the body the evidence and the
   options, `involves` the concepts it concerns. It stays open across sessions, the Atlas Health
   panel lists it for the operator, and the next session's step 1 reads it back. Run `gate_check` with `changed_ids` set to the concepts you wrote.
   Write responses surface per-concept findings inline; the `gate_check` at session end is the
   complementary pass.
5c. **Ask, then record what nobody can answer yet.** An item only a person can settle (a fact the
   KB does not hold, duplicates neither of which contains the other, a choice between sources that
   disagree). Assisted: ask the operator now, and write the answer. Record a gap only when they
   cannot answer in the session ("I have to check", "ask X"); delegated: record it without asking.
   A gap makes the wait visible; the item stays listed until the answer is written:
   - `search` first for an open gap on the same concept and question; if one exists, `concept_patch`
     it with the new evidence instead of opening a second;
   - otherwise `concept_new` a `Contradiction` (map: the KB's contradiction map) with
     `contradiction_kind: open_question` (a question raised) or `missing_context` (the KB lacks
     information it should have), `resolution_status: open`, `involves` the concepts, `title` the
     question in one sentence, the body the evidence and the options. Gaps never block a gate
     (D273); `kb_status` `open_gaps` is the queue of what people have to answer;
   - the next session meets the item again, finds the open gap in step 1 and does not ask twice.
     Count the item against the budget.
   Closing a gap later (the answer arrives) is `concept_patch` (`resolution_status: resolved`), or `conflict_resolve` when the client can call it (not in `tools/list`); step 1 reads the open ones back.

5b. **Harvest and archive.** `kb_review` `kind: "harvest_candidate"`: a journal entry that is closed
   and older than the journal's `harvest_after` (default 45 days). Extracting the durable facts is
   a mid-tier reader job (Delegation section); the write-back is yours. For each:
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
   A `lint` finding `digest_missing` is the signal that this digest is missing for a journal quarter
   (archived entries no `digest` of the journal links): write it as above.
   `kind: "lesson_unpromoted"`: an entry already archived that no live page links. Same procedure
   (carry the facts with a dated link back); an entry with nothing to carry gets the header line
   `> Archived YYYY-MM-DD: no durable lessons.` - that wording is the kind's only exemption, so
   change it here and in `lesson_unpromoted` together. Never delete the entry.
   `kind: "index_unclassified"`: `index_get` the map, then `index_patch` each entry from
   `## New pages` to the section it belongs to (or drop it if the page needs none); the heading
   goes when empty.
   An entry with no durable facts is still archived, with "no durable lessons", and the digest
   records its outcome. Delegated (D305): extract the facts yourself and report what you wrote;
   assisted: show the candidate and the proposed lessons for the operator to confirm. Archived
   entries leave default `search`, `read_cost` and the atlas structure; `search` with
   `include_archived: true` still finds them.
6. **Findings.** `lint`: every finding is a problem, none is advice (D947), and the session is done only when
   nothing is left: nothing accepts a finding (D907), so each one is fixed on the page. Fix what is a defect (`orphan`: link the page with `link_suggest`; `bare_link_list`:
   one reason per link; `link_to_retired` in a live page: update the sentence or point at the
   successor; `index_lists_retired`: remove the entry; `wait_undated`: set `review_after` to the day the wait
   should come back, or drop `waiting_on`; `superseded_target_retired`: `kb_repair` points `superseded_by` at the end of the chain; to retire a page use `concept_archive` when the client can call it, otherwise patch + move + `kb_repair`, see "Tools not in `tools/list`"). A finding
   that is wrong for a page that is right as written is a defect of the check, not of the page:
   leave the page, and list the check, the page and why in the closing report so the operator
   can raise it upstream; never add a field, a heading or an empty section to silence it. A
   leftover `lint_ignore` on a page or `lint_accept` in `instructions.md` silences nothing any
   more: `kb_repair` drops them. Many per-concept writes go in one `concept_batch` (up to 50
   operations, one commit), not one call each.
   - **Page names** (D315): `title_h1_mismatch` is mechanical (`kb_repair`, the heading follows the
     title); `title_quality` has a fix only for decorative characters, because the wording is a judgement:
     propose a shorter label (assisted: show the operator); a title that is right as written is a
     check defect for the closing report.
   - **Drift** (D357): `stray_file` (a non-Markdown file in `data/`) and an `unknown_type` with no unique
     case match are judgement: delegated, decide from the KB and the palette the message
     lists (assisted: show the operator the file or the type); then move the file (`asset_write` under an expanded concept if it belongs to
     one) or `concept_patch` the type; a file you cannot place is a gap (5c), never a deletion. A tie in `value_case_variant`: the spelling most
     pages use wins (assisted: ask), then patch the minority.
   - **Boilerplate** (D314, D317): a `repeated_fact` whose evidence line comes from a template is
     structural (the archival banner of 5b is never one: an archived entry's is skipped). The fix is in the template (`artifact_read` `templates/`), not in each concept:
     one decision, not one per copy.
   - **Search misses**: `kb_status` `search_misses`. A miss with `resolved: true` found something
     on the last check and needs nothing (D319); a still-open one, asked more than once, is a gap:
     write the page if the KB has the facts, otherwise an `open_question`.
7. **Artifacts.** `artifact_read` the KB's `instructions.md` and `templates/`: update any rule or
   template this session made obsolete (a field renamed, a workaround a repair removed, a rule to add
   pages to a now-generated index or to add reverse links).
8. **Close.** `log_append` whose text contains `kb-doctor` and the before/after counts per bucket,
   for example `kb-doctor: findings 205 -> 0, fixable 40 -> 0, review 34 -> 25, open_markers 161 ->
   158`. `kb_status` reads `last_doctor` from it, which also stops the proposal for one interval.
   Delegated, the entry is the transparency report the operator reads instead of being asked: list
   **every decision** (what was merged, moved, closed, written and why), **every gap opened** with
   its ID and question, and the before/after counts; then tell the user in a few lines the same.
9. **Volume.** A KB imported with hundreds of findings is not special: do not invent a procedure.
   Each session works in this order and closes when the budget is spent: the mechanical repairs
   that change the graph first (link forms, frontmatter normalisation: many findings disappear
   after them, so re-run `kb_status` to recount), then the body-text checks (`bare_link_list`,
   `link_to_retired`) in `concept_batch` calls of up to 50 operations, one commit each, fanned out
   per the Delegation section (cheap-tier writers on disjoint maps, the coordinator gate-checking
   every id), then `kb_review` items from the heaviest. The queue empties over
   the sessions the operator starts.

## Rules

- Never start a session the operator did not ask for or agree to.
- Assisted: judgement is never applied without the operator's choice, nor a mechanical repair
  the background repair does not run without a dry-run plan they saw. Delegated: you choose, under the allowed
  and never lists in Purpose, and every choice is in the closing report. The operator can hand a
  session over ("do it yourself") at any point: from there it runs delegated.
- One numbered list per session; a deferred item is not asked again in the same session.
- Never overwrite by hand a concept `kb_repair` skipped as changed: re-read it and run the check
  again. A fix skipped as needing a person is written by hand, after reading the concept.
- Never delete a journal entry during archival, and never move it out of its journal.
- The quarterly digest is created if it does not exist and updated if it does; its status is
  `reference`, never `archived`.
- Do not invent contract values, glossary definitions or procedure steps.
