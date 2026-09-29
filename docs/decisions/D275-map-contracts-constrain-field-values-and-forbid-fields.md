---
topic: data-plane
---

# D275 — a map contract can constrain field values and forbid fields

**Decision.** A map's `_map.md` accepts `field_values.<field>: [v1, …]` (every concept), `field_values.<Type>.<field>: [...]` (one type, replacing the map-wide list for that field) and `forbidden_fields: [f, …]`. Lint reports `invalid_field_value` and `forbidden_field`, both error severity and not suppressible with `lint_ignore`. `map_create` and `map_update` carry the three keys. Values compare as exact trimmed strings, a list value is valid only if every element is allowed, an absent field is not checked, and an empty allowed list is a malformed key.

**Why.** Vocabularies such as `status` or an incident `outcome`, and retired field names, lived only as prose in a KB's instructions and drifted. Every such rule is deterministic, so it belongs in the contract rather than in the agent's memory. It stays a lint contract, not a write gate, exactly like `required_fields`: `gate_check` already fails on error findings, so the agent loop catches it before finishing. The cost is that a violating concept can still be written until lint runs.

**Alternatives rejected.** A KB-wide contract file: needs a new root file for a handful of repeated lines, and the contract already lives in `_map.md`. A per-type list that extends the map-wide one: a per-type vocabulary is usually a different vocabulary. Making the checks suppressible: a concept could declare the contract void. A write-time gate: inconsistent with `required_fields` and it would block bulk repairs of drifted KBs.

**Consequences.** Existing KBs are unaffected until a map declares the keys. Field names and type names in these keys cannot contain a dot (the key syntax is dotted). `retired()` in `internal/lint/structure.go` still hard-codes `deprecated`/`superseded`; a typo there is now catchable by declaring `field_values.status`.
