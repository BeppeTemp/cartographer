---
topic: skills-services-secrets
---

# D104 — Structured SOPS pointers, scoped refs and encrypted-only writes

**Decision.** Decrypted YAML is flattened to RFC 6901 JSON Pointers and
`secret_refs` use scalar `NAME=path#pointer` entries. `secret_set` updates an
existing encrypted secret with `sops set --value-stdin`; it never creates the
root `.sops.yaml` creation-rules file.

**Rationale.** Repeated nested leaves otherwise collide in a flat map. Pointer
references provide least privilege without widening the intentionally small
frontmatter grammar. Creation rules and recipient selection remain operator
work, while encrypted `*.sops.yaml` files are safely writable by the server.
