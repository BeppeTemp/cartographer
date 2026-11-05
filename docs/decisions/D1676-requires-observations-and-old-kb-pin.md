---
topic: sync-provisioning
---

# D1676 — `requires` suggestions are observations on an artifact file, and a pre-feature KB is pinned

**Decision.** `kb_review` lists two observations about `requires` (D1661): `requires_unresolved` (a `<kind>:<name>`
entry of a skill, agent or hook naming no artifact of this KB nor a bundled skill) and `requires_undeclared` (a skill
body names a same-KB agent as a whole word outside code, and its `requires` lacks `agent:<name>`). Neither is a lint
check: the first reference may come from another KB bound to the same client, the second is a word search. A malformed
entry is `requires_invalid`'s, so the detectors skip it; capability ids are never judged.

**Review item shape.** They are about a file, not a concept: `ReviewItem` gains the optional `artifact` (KB-root
relative path) and carries an empty `concepts`. Artifacts are whole-KB resources, so they are whole-graph items: only a caller
that sees the whole KB receives them, and a partial-scope caller never learns an artifact path or agent name.

**Old-KB pin.** `TestOldKBOutputIsUnchanged` (internal/lint) builds a pre-feature KB (2 skills, 2 agents of which one
`strict_tools`, 1 hook, no `requires`) and pins that lint gains no `requires` finding and that kb_review gains exactly
the one suggestion the heuristic is meant to make. The Claude Code projection golden of the same KB is the one of the
degradation slice (`internal/provisioning/testdata/golden/claude-code.txt`), not duplicated here.
