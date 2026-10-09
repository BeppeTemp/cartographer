---
topic: data-plane
---

# D357 — The drift audit closes the blind spots

**Decision.** Five shapes a KB drifts into become lint checks (`unmapped_folder`,
`unknown_type`, `value_case_variant`, `stray_file`, `repeated_link`), three
existing checks get the fix they lacked or a better one (`missing_title` now sets
the title; `title_h1_mismatch` copies the heading into a title that
`title_quality` rejects instead of the reverse; `stringified_list` fires on any
string in a list field), and a write's commit holds only the write: whatever the
working tree held before is committed first as `external changes`.

**Why.** A drift audit on scratch KBs found a shape no check reported, two fixes
that were missing and one that destroyed information (`kb_repair` rewrote a good
heading to `# 🚀`), and a `kb_repair` that applied nothing yet committed ten
unrelated files under its own name. The unattended doctor is credible only
if its commits say what it did. The cost is one extra commit on a KB edited
outside MCP, and a few more findings on a KB that predates the checks (all
warning or info).

**Alternatives rejected.**
- Commit only the handler's paths (`CommitPaths`): generated indexes (D301) and
  moves touch paths the handler does not return, so the list would need its own
  accounting; committing the earlier state first is exact.
- Refuse a write on a dirty tree: an editor open on the KB would block every agent.
- Always sync the H1 to the title (D315 as it was): destroys the good side when
  the title is the one at fault.
- Split `provenance` on commas like `tags`: a source citation may contain one.
- Make `unknown_type` fire on every non-canonical type with no palette: a KB with
  no templates and no strict map has no vocabulary to deviate from.
- Fix `stray_file` by moving or deleting it: where a file belongs, or whether it
  matters, is judgement.

**Consequences.** `unknown_type` runs only when the palette (types of
`templates/*.md` plus `concept_types` of strict maps) is non-empty. `unmapped_folder`
repairs a folder, not a concept: it has its own planner and applier
(`planMapRepair`, `applyMapRepair`), runs first in the heartbeat and never in
repair-on-write, and the scaffold is `kb.ScaffoldMap`, which shares its file
writing with `map_create`, so the two cannot produce different maps. Four of the
checks are `AutoRepairSafe` and so join `DefaultAutoRepair`
(`missing_title`, `repeated_link`, `value_case_variant`, `unmapped_folder`).
`external changes` is skipped while git conflicts are open, and the write gate
(D350), which skipped a dirty tree, now sees a clean one. Any future check that
changes how a title is judged goes through `titleTextIssues`, which both title
checks share.
