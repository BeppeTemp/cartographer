---
topic: data-plane
---

# D356 — Invalid pages are repairable

**Decision.** The validation errors of a page (no frontmatter, unparseable
frontmatter, no `type`, deeper than three segments) are lint checks, so
`kb_repair`, the heartbeat, `kb_review` and the doctor see them. Where the answer
is unique they carry a deterministic fix: `add_frontmatter` and `missing_type`
use the type every other typed page of the map already shares (at least two,
all equal), `quote_value` double-quotes the one line that breaks the block.
Repairs write through `kb.RepairConcept`, whose validity rule is "the errors
after are a subset of the errors before", so an unrelated fix is no longer
blocked by a page's missing type. Two warnings join them: `nonslug_file_name`
(fix: `move` to the slugified ID, through `concept_move`'s code path) and
`empty_concept` (no fix).

**Why.** A KB in bad shape could not be repaired at all: every repair went
through the validated write path, and the validation errors themselves had no
repair, so three `kb_repair` rounds applied zero fixes on such pages.
`RepairConcept` is the alternative to "fix validity first, then everything
else", which blocks every other fix on any page whose type cannot be resolved.
The cost is a second write path with a weaker rule.

**Alternatives rejected.**
- Most common type in the map: a majority is a guess, and a wrong `type`
  silently changes contracts.
- Relax `WriteConcept` for everyone: agent writes must keep full validation.
- Drop the mirrored lint errors and keep `validate` alone: the repair and the
  doctor never read `validate`.
- Fix `concept_too_deep` by moving: where a page belongs is judgement.
- Delete an empty page: no automatic deletion, ever.

**Consequences.** `RepairConcept` is used only by `kb_repair`, the heartbeat and
repair-on-write, never by an agent-facing write tool (a test pins that
`concept_write` still rejects an invalid concept). A repair never widens the
error set (a test). No invented content: only derived values (an H1, a file
stem, a type every sibling uses). `gate_check` lists a validation error once:
the three lint checks that mirror `validate` are dropped when the same path is
already in `validation_errors`. The four fixes are `auto_repair` defaults;
`nonslug_file_name` runs in stage 2 of the heartbeat (D355) because it touches
other files.
