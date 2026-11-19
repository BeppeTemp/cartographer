---
topic: control-plane
---

# D2280 — an edge case warns or falls back, it does not refuse or pass in silence (#2278)

**Decision.** Three edge cases that were silent or a dead end now say what they are without refusing. `gate_check` adds an optional `warnings` list (omitted when empty, like the `warnings` of `map_create` and `map_update`) naming each `changed_ids` entry that is no concept and never was in the KB's git history; `pass` is unchanged. `map_create` and `map_update` add to their `warnings` each template slug (`templates`, `default_template`) with no parseable `templates/<slug>.md`; the template catalog is the only definition of a known template and the call still succeeds. `cartographer kb secrets init` writes the creation rule in the KB's clone itself (open the clone, process lock, `CommitPaths`, best-effort push, the commit subject of `secrets_init`) when the health call fails with a refused connection, and only then; an HTTP answer (a 401 included) or a timeout keeps the server path and its error. This amends D2039, whose command no longer needs the service.

**Why.** A gate that checked nothing for a typo read as green, a map naming a template that did not exist waited until its pages reported `template_missing`, and the "local" command failed for want of a service the operator had not started. Each has a legitimate twin that a refusal would break: a deleted or moved id, a template written after its map, a service that is simply down. A warning is the minimum that tells the cases apart; the cost is that a caller must read a field it did not have to.

**Alternatives rejected.** `pass: false` for an unknown id: a gate that checked nothing is not green, but a session end that passes ids it deleted in a shallow clone would break. Rejecting an unknown template: breaks map-first-then-template and preset flows. A map-level lint finding for it: a new check that fails the gate on existing KBs, a feat. Always writing the rule directly: skips the admin check of the server path. Only a better error message: the issue stays.

**Consequences.** `warnings` of `gate_check` is additive and never a failure; an absent id with history says nothing, no git or no commits warns that a deletion cannot be told from a typo, and at most 50 absent ids are examined. The direct path is the family of `kb reauthor` and `kb attach` (D155 lock, not raw git by hand, D323) and runs `sops.InitRule` and the same `runKBSecretsInit` checks, so output lines and exit codes do not change; a push that fails is printed, not fatal.
