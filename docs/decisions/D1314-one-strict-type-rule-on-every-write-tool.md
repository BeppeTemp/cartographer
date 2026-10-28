---
topic: control-plane
---

# D1314 — One strict-type rule on every write tool: refuse an introduced undeclared type, never a type kept

**Decision.** In a strict map (non-empty `concept_types`) every write tool, `concept_write`, `concept_new`, `concept_patch` and `concept_batch`, refuses a write that introduces a type the map does not declare: a new page, or a change of `type`. A write that leaves the type of an existing page as it is passes, even if that type is already undeclared. The refusal names the declared types and points to changing `type` or `kb_repair`. Moves and transfers into a strict map keep their own target check.

**Why.** The check lived only in `concept_batch`'s preflight, so one write was accepted by one tool and refused by another, and a batch patch of an already-invalid page was refused while the same `concept_patch` succeeded. The rule `kb.RepairConcept` already follows is that an edit which does not make a page worse is never refused.

**Alternatives rejected.** Making `unknown_type` an `OnWrite` check judged by the write gate: it is a KB-wide palette check whose message names every type, and the gate does not run on a KB without git, where the rule must still hold. Keeping the batch preflight as it was and adding the plain check to the single tools: it would keep refusing a patch of an already-invalid page.

**Consequences.** `introducedTypeViolation` (`internal/mcpserver/tools_write.go`) is the one place of the rule; a new write tool must call it (`writeConceptAndLog` already does). A page of an undeclared type can still be edited, and leaves that state only by a type change to a declared type.
