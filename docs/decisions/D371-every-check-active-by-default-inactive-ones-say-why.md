---
topic: data-plane
---

# D371 — Every check is active by default; one that cannot run says why

**Decision.** A lint check runs unless it has nothing to compare against on this KB or every map able to run it opted out. The one opt-in that flips is the template family (D352): a map that declares `templates` or a `default_template` is strict without `require_template: true`, and `require_template: false` is its opt-out. `lint.Inactive` computes, per KB, the checks that cannot run and why; `GET /api/ui/v1/kbs/<kb>/checks` returns `active` and `reason` for each, and the Atlas Health coverage greys an inactive check out instead of showing a zero that would read as clean.

**Why.** The Health coverage (D365) lists every check with its count, so a reader takes a zero for "checked and clean". For the template checks, `unknown_type` and a handful of others that zero meant "not checked". Inventory of the gates on emission, each classified:

- opt-in that flips: `template_*` (gated on `require_template`);
- already active by default with an opt-out: `stale_open` (journal 60 days, work map 30, `stale_after: 0` off, D346), `open_marker`, `title_quality`;
- real precondition, stays gated and reports `active: false`: `unknown_type` (no type palette), `forbidden_term` (no forbidden term), `source_uncited` (no `Source`), `sops_missing_file` (no `secrets/`), `legacy_path` (no `legacy_paths`), `unknown_placeholder` / `unused_placeholder` (no `paths.yaml`, with `missing_registry` as the nudge), `missing_required_field` / `invalid_field_value` / `forbidden_field` (no map declares the key), `index_stale` (no generated index), `template_*` (no strict map);
- noisy by nature, kept gated: `index_incomplete` raises one finding per page of a map that never promised a curated index, so it stays tied to `require_index_entry`; the honest default is `index: generated`, which makes the list complete by construction.

**Alternatives rejected.** Flipping `index_incomplete`: a finding per page on every map is the flood the issue warns against. A migration step that writes `require_template: false` into existing maps: it would freeze the old behaviour and hide the gap the change exists to show; newly active findings report normally, the background repair absorbs what is `AutoRepairSafe` (`template_missing` with one candidate, `template_type_mismatch`, aliases, order) and the doctor the rest. Keeping `active` out of the API and only greying in the UI: the reason belongs to the server, which alone knows the contracts. A per-check `enabled:` KB setting: `lint_ignore` already silences a check per map or page.

**Consequences.** `require_template: false` is written, not deleted, by `map_update`: absence now means "default". A new check with a precondition adds its entry to `lint.Inactive` (`CONTRIBUTING.md` §Adding a lint check). `active` is per KB, not per map: a KB with one strict map and one opted-out map reports the template checks active, and the opted-out map's zero is not "clean" for that map; `map_without_templates` remains the per-map nudge.
