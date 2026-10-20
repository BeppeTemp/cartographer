---
topic: control-plane
---

# D907 — The lint cannot be escaped: a write never introduces an avoidable finding, and nothing hides one

**Decision.** No setting, key or call switches a lint check off, loosens it or hides a finding.

- **The write gate is fixed behaviour.** In every KB that is a git repository, after the server has applied the
  safe mechanical fixes to what the call wrote (always, in the write's own commit), a `concept_*` write (but
  `concept_transfer`, which commits two KBs itself, D760) or `supersede` is refused and rolled back when it
  **introduces** a finding of a check marked `AvoidableAtWrite`, at any severity. The refusal lists each finding with its fix hint. A finding the touched
  concepts already had never refuses a write. Nothing accepts a finding: `accept_findings` and the
  `Accepted-Finding` trailer go (old trailers stay readable in `concept_history` and `changes_since`).
- **Avoidable** means one call could have avoided it, with `concept_batch` atomic: create the link target or drop
  the link, list the page in the index or link it from its parent, link instead of copying, write a checkbox
  instead of a TODO, split the page now, fix the field, value, title or template. A check is not avoidable when
  the finding comes from time (`stale_open`, `stale_claim`, `artifact_unused`), from growth (`map_oversize`,
  `facet_sprawl`, `missing_value_contract`, `map_without_templates`, `island`, `cut_concept`, `map_misfit`), from a legacy shape (`legacy_archive_descriptor`,
  `contract_legacy_key`), from a step another tool takes (`source_uncited`, `orphan_asset`,
  `unused_placeholder`, `imported_draft`), from the KB's initial state (`missing_instructions`,
  `missing_registry`) or from what the server regenerates itself (`index_stale`). What the write path refuses
  before lint (`missing_frontmatter`, `unparseable_frontmatter`, `missing_type`, `concept_too_deep`, D356) needs
  no gate. A check that is not avoidable says why in `docs/data-plane.md`, and a test fails on one that does
  not, so a new check is classified on purpose.
- **Every exception goes:** `lint_ignore` (concept and `_map.md`), `lint_accept` in `instructions.md`, the
  `lint_ignore_invalid` check, the suppression inventory, `CheckAcceptability`, and the contract keys that turned
  a check off or moved its threshold: `require_template`, `template_sections`, `require_index_entry`,
  `machine_path_allow_prefixes`, `title_max_length`, `repeated_fact_min`, `hotspot_in_degree`, `hotspot_bytes`,
  `oversize_bytes`, `oversize_concepts`, and `stale_after: 0` / `harvest_after: 0`. `ontology_mode` is derived
  from `concept_types`; `required_fields` and `field_values` have one shape each, keyed by type with `*` for
  every type.
- **Old keys are realigned, never honoured.** The new check `contract_legacy_key` (warning, auto-repair safe,
  fix `rewrite_contract`) reports a removed key in a `_map.md` or `lint_accept` in `instructions.md`; its fix
  drops the key, derives the mode and merges the flat shapes into the `*` entry, so the unattended repair
  realigns a KB in one commit. `lint_ignore` in a concept is a `nonstandard_field` the repair drops.
- **Loosening a contract is allowed, with a reason.** A `map_update` that drops a required or forbidden field, a
  field's allowed values, the type list or the templates, or widens one, is refused without the write's
  `reason`; with one it commits with the `Reason` trailer and `log.md` records `loosened: … (reason: …)`, which
  the Atlas Activity shows.
- **A `kb_review` item closes on page state, never on a dismissal.** Each kind closes when the pages it names
  are in the state it asks for: `duplicate_candidate` merged or linked both ways (D854); `zombie_work` the work
  closed or its origin link retargeted; `promotion_candidate` a link into `promote_to`; `glossary_gap` the term
  in `glossary.yaml` or a glossary map; `repeated_fact` the line kept in fewer than three concepts;
  `read_hotspot` a smaller body or fewer inbound links; `scattered_work` a link into `work_map` or the items
  closed; `harvest_candidate` the entry archived; `status_reclassify` the status changed or listed open;
  `map_naming` titles that share one scheme and are not too long; `template_proposal` the map declaring
  templates its pages follow; `lint_judgement` the check no longer reporting. A test writes each closing state
  and requires the item to go; a new kind needs one. A reason no page state could settle is not a review item:
  the title a folder does not echo (D315) is only `map_create`/`map_update`'s write-time warning now, because a
  title in another language than its folder may be right as it stands.

Supersedes D313 (acceptability), D332 (`lint_accept`), D834 (the suppression inventory) and D350 (the opt-in gate
and `accept_findings`). Amends D306 (a finding is fixed, no longer accepted), D159 (the per-concept opt-out and
the allow prefixes; its size thresholds stay), D349 (repair on write has no opt-out), D371 (`require_template:
false` is gone; a check that cannot run still says why) and D315 (the folder clause of `map_naming`).

**Deviations from the plan (#907).** The plan made a review reason no page state can settle "doctor advice shown
once per session"; the one such reason found, the folder clause of `map_naming`, already had a better home in
the write-time warning of `map_create`/`map_update`, which reaches the writer while the title is being chosen,
so no new advice channel was built. Putting the folder reason back as an item is a one-line change in
`poorMapTitles`, should a page state for it appear. The plan also removed the server keys in the same change; it
lands in two halves instead, the behaviour first and the keys after #906's settings registry (see Consequences).

**Why.** Each opt-out was added for a good local reason, and together they let a long-lived KB be configured
almost at random: a Health panel with no findings could mean the pages are right or that something was set
aside, and nobody could tell which. A false positive silenced on one page stays wrong on every other KB; reported
as a defect, the check is fixed for all of them. Refusing at write time is the cheapest point to prevent a
finding: the writer has the page open, and one batch can fix it. The cost is that writes are refused more often,
most visibly a new page that its map's curated index does not list yet, and that a map whose rule is wrong must
change the rule, in the open, rather than step around it.

**Alternatives rejected.**
- Keep the gate as a setting and turn it on by default: a switch is an escape, and the KBs that most need the
  gate are the old ones that would keep it off.
- Gate by severity (refuse errors and warnings, pass info): time- and growth-based warnings would block writes
  that did not cause them, while avoidable info findings would still pile up. Avoidability is the property that
  matters to the writer.
- Keep `lint_ignore` with a mandatory reason and audit it (D834): the inventory grew into a second list to
  review, and an accepted finding still hid the page from every later reader.
- Store review dismissals outside the pages: state outside the KB is invisible in git, diverges between
  replicas, and outlives the page change that should reopen the item.
- Keep the folder clause of `map_naming` with a "title is fine" key: an exception by another name.

**Consequences.**
- A new lint check states `AvoidableAtWrite` deliberately; a new `kb_review` kind comes with its closing page
  state and a closer in the trap test.
- Tests and tools that write a page on its own into a curated map must list it in the index (or link it from
  its parent) in the same call, or use a generated index.
- A future knob that would turn a check off, raise its threshold per KB or hide a finding contradicts this
  decision; a threshold that is wrong is changed as a constant, for every KB.
- The gate's refusal for a caller who cannot see the whole KB is judged as for anyone, but reports a whole-graph or artifact finding with the check and the visible page only, and fix targets cut to visible pages: the refusal names no page the caller cannot read.
- A delete plus an add that git does not report as a rename is treated as a move only when the two pages share frontmatter `title` and `type` (and the file name is unique among the call's deletions and additions); otherwise the added page is judged as new.
- The heartbeat always runs `contract_legacy_key` and the drop of a `lint_ignore` field from pages whatever `auto_repair` lists, so a KB with a customised or empty list is realigned too. Every other `nonstandard_field` rename (state to status, and the like) runs only when the KB lists that check: `auto_repair: []` still means no other unattended page rewrite. The list keeps governing the other checks until Part B removes the key. Stated deviation: `doctor_auto_interval: 0` still turns the heartbeat off, and with it the realignment, until Part B removes that off switch.
- A page keeps a field its map's contract names: `nonstandard_field` does not report, and the safe repair does not rename, a synonym that the page's map requires or lists in `field_values`. A contract requiring `updated` therefore never conflicts with the repair, and a page write never edits `_map.md`.
- `write_gate` and `repair_on_write` are still read but act on nothing; they leave the settings files, with
  `auto_repair` and the doctor intervals, in the second half of this change, after the settings registry of
  #906 (#890).
