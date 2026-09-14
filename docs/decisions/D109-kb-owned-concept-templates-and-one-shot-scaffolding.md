---
topic: data-plane
---

# D109 — KB-owned concept templates and one-shot scaffolding

**Decision.** Templates are plain Markdown artifacts at `templates/<slug>.md`, maintained through the artifact lifecycle and discovered with `template_list`. They are not concepts: they have no ConceptID and stay outside `WalkConcepts`, search, lint and the graph. `concept_new(template, id, vars)` renders a validated template into a new concept with literal, single-pass substitution; it never overwrites or curates a map index.

Templates are deliberately excluded from the provisioning manifest, revision, lockfile, provider matrix and pruning. They are KB content for server-side page creation, not instructions or configuration to materialize into a client. This is the intentional divergence between the artifact whitelist and provisioning kinds.

Template variables use only `{{identifier}}`. The syntax is disjoint from D75's client-side `{{repo:key}}` and `{{path:name}}`: colon forms are rejected so a provision-time placeholder can never silently survive into a concept. Rendering has no logic, includes or repeated interpretation; inserted values are literal.

**Alternatives rejected.** Templates as concepts in a dedicated map would pollute the graph, indexes and lint; putting templates in a map descriptor would couple reusable shape to one location; adding a template parameter to `concept_write` would blur its full-content, `if_match` update contract. D107 remains complementary: templates prescribe a starting shape, while map contracts deterministically report required-field and curated-index conformance.
