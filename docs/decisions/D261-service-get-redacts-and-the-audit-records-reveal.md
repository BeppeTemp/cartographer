---
topic: skills-services-secrets
---

# D261 — service_get redacts resolved secrets unless reveal, and the audit records reveal

**Decision.** `service_get(resolve_secrets: true)` has the same contract as
`secret_resolve` (D158): it lists the key names with `<redacted>` values, and a
new boolean `reveal: true` returns the values. `reveal` without
`resolve_secrets` is a no-op, not an error, and adds no authorization check —
`resolve_secrets` still requires `rw`. The audit trail records allow-listed
boolean arguments as `"true"`/`"false"`, so `secret_resolve`'s `reveal` and
`service_get`'s `resolve_secrets` and `reveal` now reach it. The dead
`sops.EnvForSkill` is deleted. `rw` scope on a KB is documented as authority
over every secret that KB can decrypt. Amends D158.

**Why.** D158 left `service_get` printing values because "its callers are the
skill-execution path, not an agent transcript". That path did not exist:
`EnvForSkill` had no caller outside its own test, and the only caller of
`service_get` is an MCP client — the exact leak D158 closed on the sibling tool.
D158 also said `reveal` "is recorded in the audit trail", but `extractResources`
kept only string values, so the boolean was dropped silently: the one event the
decision wanted audited left no trace. The cost: a caller that parsed values out
of `service_get(resolve_secrets: true)` breaks until it passes `reveal: true`.

**Alternatives rejected.**
- *Keep `service_get` in clear, fix only the audit* — leaves the safe default as
  an opt-in on one of the two tools that print credentials.
- *Error on `reveal` without `resolve_secrets`* — nothing is revealed either way;
  a refusal would only make a harmless flag combination fail.
- *Record every boolean argument in the audit* — the allow-list is the only gate
  on what reaches the trail; a type-based rule would admit fields nobody chose.
- *Wire `EnvForSkill` into a skill-execution path* — it would build a feature to
  justify the exemption instead of removing the exemption.
- *A dedicated secrets scope (`kb:<name>:secrets`)* — `rw` can already
  `concept_write` a `secret_refs` entry pointing at any
  `secrets/*.sops.yaml#/pointer`, so `secret_refs` bound what is returned, not
  who may read. A separate scope is real access-control work (it would have to
  cover `concept_write` of `secret_refs` too) that today's single-operator
  deployments do not need; the boundary is stated instead.

**Consequences.** Every tool that decrypts a secret redacts unless `reveal: true`;
a new one must follow the same contract and list its revealing flag in
`auditResourceFields`. The audit allow-list may now name boolean fields; secret
key names (`names`, `secret_set`'s `key`) and values still never enter it. A KB
whose secrets must be readable by fewer principals than may edit it needs its own
KB, key and token. `sops.validatePath` checks a path before `sops` opens it; the
window lets a writer on the KB filesystem swap a component, which is accepted
because that writer can already rewrite the KB.
