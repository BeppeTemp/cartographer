---
topic: control-plane
---

# D161 — Every enforced limit and contract stated in its own tool description

**Status: implemented (2026-08-28).** Closes #182.

**Context.** A tool description is the **only** documentation an agent reads. Every limit
missing from it is discovered by failing, and every alternative missing from it is not
discovered at all.

- A refactor across ~620 concepts produced **760 commits**, one per `concept_patch`.
  `concept_batch` is exactly the right tool — atomic, one commit — but it is `advanced`
  (deliberately, D125), so an agent working with the default tool set cannot see it, and
  `concept_patch`'s description said nothing about it. The tool remains callable by name,
  which is how the field migration eventually used it; what was missing was the signpost.
- `internal/kb/kb.go` describes `MapContract` as the *lint* contract of a map, while
  `map_create`'s schema said `required_fields` were "required for every concept in this map".
  "Required" reads as a write gate. It is not: a missing field is a `missing_required_field`
  finding and the write succeeds. Learning which one it was meant reading Go.
- Four enforced limits appeared in no description: 1 MiB per asset, 50 operations and 512 KiB
  aggregate per `concept_batch`, 500 lines per skill body. The batch numbers matter for exactly
  the work `concept_batch` exists to do — a refactor touching several hundred concepts must be
  planned in chunks of 50, and an agent that does not know the number discovers it by having a
  batch rejected.
- The skill-body message reported the overage in one unit and the budget in another: "body
  exceeds 500 lines (N lines); keep under 5000 tokens".

**Decision.**

- A limit is stated as a **number**, not as "bounded" or "large": a number is actionable, an
  adjective is not.
- **The numbers are not duplicated as literals.** Descriptions are built with `fmt.Sprintf`
  from the same constants the handlers check, through one `byteBudget` helper so `512 KiB` and
  `1 MiB` render consistently. This is the one structural part of the change and it is what
  makes the rest safe: the tests read the constants too, so bumping a constant cannot leave the
  text stale.
- **`concept_patch` names `concept_batch` and says it is callable by name** even though
  `tools/list` omits it. Naming a tool the agent might not see is deliberate: that it is
  callable is true and is precisely the fact an agent cannot otherwise discover.
- **`concept_batch` is not promoted out of `advancedToolNames`.** The field report suggests
  considering it; D125's rationale (keep the default working set small) still holds, and the
  signpost solves the actual problem, whereas a promotion would change every client's
  `tools/list`.
- The three map-contract fields are described as **lint contracts, not write gates**, and
  `ontology_mode: strict` is named as the one contract that does fail a write — the asymmetry
  is what made the area confusing, and stating it once resolves all of them.
  `require_index_entry`'s description also states that both link forms are accepted for an
  expanded concept (D149 WP3), since that is the single most misread rule in the report.
- The skill message reports **lines only**. The token figure is removed rather than converted:
  a token count depends on the tokenizer and `internal/skill` is deliberately dependency-free.

**Consequences.** Documentation only, but it changes every `tools/list` payload — the affected
descriptions grow by a clause or two, which is the cost of an agent not having to fail to learn
a limit. No behaviour changes.
