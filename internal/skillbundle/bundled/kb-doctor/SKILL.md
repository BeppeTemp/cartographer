---
name: kb-doctor
description: Align an existing Knowledge Base with the current Cartographer standard - apply the mechanical fixes with kb_repair and walk the judgement ones. Use when kb_status reports conformance.doctor_suggested, when the operator asks to tidy or align a KB, or after a Cartographer upgrade.
version: "1.0"
---
# KB Doctor - Skill

## Purpose

A KB drifts from the standard the server reads: synonym fields (`updated` for `timestamp`), tool
parameters stored as fields, links to retired pages, concepts in the wrong map. Lint reports it,
`kb_status.conformance` counts it, and this procedure repairs it. Nothing runs by itself: repair is
an explicit, dry-run-first call (D290), and a KB that was repaired can drift again, so the truth is
always the next `lint`, never a stored version.

## Procedure

1. **Check the signal.** Call `kb_status` and read `conformance` (`findings` by severity, `fixable`,
   `last_doctor`, `doctor_suggested`). Stop if `doctor_suggested` is false and the operator did not
   ask for a pass.
2. **Survey.** `lint` over the whole KB, grouped by check (`counts_by_check`). Tell the operator what
   the pass will cover.
3. **Mechanical fixes.** For each check that emits fixes (`nonstandard_field`, `tool_param_field`):
   `kb_repair` with `dry_run: true` (the default), show the plan to the operator, then repeat with
   `dry_run: false` after confirmation. One call is one commit. A concept in `skipped` changed since it
   was listed: run the check again.
4. **Judgement fixes**, in order of value. Batch per map and run `gate_check` scoped to the map after
   each batch:
   1. `broken_relation` / `broken_link`: read the concept, fix or remove the reference.
   2. `missing_value_contract`: propose the `field_values` to the operator, then `map_update`.
   3. `nonstandard_field` where both fields are present: merge the values, drop the synonym with
      `concept_patch` (frontmatter key set to null).
   4. `link_to_retired`: reword, or point to the successor.
   5. `map_misfit`: `concept_move` after the operator agrees.
5. **Instructions and templates.** `artifact_read` the KB's instructions file and `templates/`: none may
   keep naming a field that was renamed. Update them in the same session.
6. **Close.** `log_append` whose text contains `kb-doctor` and the counts before and after (for
   example `kb-doctor: warnings 205 -> 0, info 97 -> 41`): `kb_status` reads `last_doctor` from it.

## Rules

- Never apply a repair the operator has not seen as a dry-run plan.
- Never overwrite by hand a concept `kb_repair` skipped: re-read it and run the check again.
- Do not invent contract values or move concepts without the operator's agreement.
