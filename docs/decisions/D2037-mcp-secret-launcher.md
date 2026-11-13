---
topic: sync-provisioning
---

# D2037 — A KB secret reaches an MCP server through a launcher, never through a client file

**Decision.** An `mcp/<slug>.json` descriptor may hold `${secret:<concept-id>#NAME}` in `env` or `headers`, and `env_literal` for non-secret constants. A reference is resolved at launch by `cartographer mcp exec` (an absolute path to the binary, an approved-hash check of the descriptor, then `secret_resolve`), put in the child's environment only, and never written to a client config, an argument list or a log; clients without header support get a stdio-to-HTTP bridge from the same launcher. This change lands only the syntax: the parser validates references and literals, and a descriptor with a reference is refused at apply until the launcher entry is emitted.

**Why.** A token kept in the KB had to be copied by hand into every shell for `${VAR}` to expand. Resolving it where the process starts keeps the secret off disk and in the server's audit trail, at the price of one `rw` read per start. `env_literal` exists so that a constant is not forced into a fake `${VAR}`; it goes through the secret scanner, because a field written verbatim is exactly where a pasted token would end up.

**Alternatives rejected.** Resolving at apply time and writing the value into the client config (a secret on disk). A new read-only scope for launches (`secret_resolve` keeps requiring `rw`; a scope is a separate decision). Allowing `${secret:` in `env_literal` (a client would expand or print it).

**Consequences.** `ParseMCPServerSpec` is the one validator and an older client's strict parse rejects the new fields: a KB adopts them after its clients upgrade. A secret reference never reaches `EmitServer`. `env_literal` values are scanned and never echoed in an error.
