---
topic: data-plane
---

# D107 — Declarative lint contracts in map descriptors

**Decision.** A map may declare `required_fields`, additive `required_fields.<Type>`, and `require_index_entry` in `_map.md`. Lint reports a missing required field as an error and an incomplete curated index as a warning; malformed recognized entries are informational. The contract is optional, so no server-default vocabulary or schema is imposed. `gate_check` fails on the error, while `validate` and contradiction-only `commit_gate` retain their existing responsibilities.

**Rationale.** A descriptor is versioned, local to the map it governs, and uses the existing string/list frontmatter grammar. It gives deterministic, auditable enforcement without executable plugins, which would make lint arbitrary-code execution and undermine its no-LLM guarantee. Required field contracts belong to governance rather than the read-path validator so legacy KBs remain consumable. Domain-specific rules (for example, required section prose or naming conventions) remain KB skill policy: generalising them would require a rule language outside this data-plane contract. `index_incomplete` deliberately complements, rather than replaces, `expanded_as_category`: the former reports each missing curated entry; the latter detects a broader filesystem-taxonomy smell.
