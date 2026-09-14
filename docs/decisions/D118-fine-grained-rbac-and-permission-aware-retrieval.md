---
topic: transport-auth
---

# D118 — Fine-grained RBAC and permission-aware retrieval

**Decision.** Authorization moves from a per-KB read/write scope to a per-principal *policy*
evaluated at a single point in dispatch. `auth.roles` declares named allow rules
(`kb` + `access: r|rw` + optional `maps`/`journals`/`types` selectors, `internal/config.RoleSpec`,
validated by `ValidateAuthRoles`); a token references roles by name and may also carry a stable
`id`. Roles compile into immutable `auth.Permission`/`auth.Policy` values
(`cmd/cartographer.scopedTokensWithRoles`) that are unioned with any legacy scopes, and the
middleware puts one `auth.Principal` in the request context. `internal/mcpserver/policy.go` resolves
every call against that policy through `resourceClassForTool`, an exhaustive registry inventory:
exact-concept, collection, source/destination, or whole-KB. Retrieval enforces the same predicate
inside the index rather than after it — `SearchFiltered`, `SearchFTSFiltered` and
`AllEmbeddingsFiltered` apply it **before** the limit. Writes are re-authorized under the git lock
via `reauthorizeUnderLock` immediately before mutating.

**Rationale.** A KB is the wrong authorization unit for a shared wiki: an agent that must write
runbooks under `infra/` should not thereby be able to rewrite every other map, and the read side is
where a leak actually happens — search, listings and semantic neighbors surface content the caller
was never meant to enumerate. Filtering *after* the limit would have been much simpler, but it turns
pagination into an oracle: a short page tells the caller exactly how many hidden concepts matched.
Selectors are intersected and there are no deny rules so that policy evaluation stays deterministic
and order-independent; adding a role can only widen access, never silently narrow another one.
Re-checking under the lock closes the window in which a concept's type changes between the dispatch
decision and the commit. `resourceClassForTool` is exhaustive rather than defaulting, so a tool
added without a deliberate choice is denied instead of inheriting whole-KB semantics.

**Consequences.** Forbidden and missing exact resources are deliberately indistinguishable: both
return `genericNotFound`, which means an operator debugging a 404 cannot tell from the response
whether the concept exists — the answer is in the role config, not in the API. FTS pagination reads
ranked rows in bounded batches (`ftsSearchBatch`) and stops at the requested limit, so a heavily
filtered query costs more reads than an unfiltered one. `AllEmbeddingsFiltered` applies the
predicate per candidate vector, so semantic search on a narrow role pays a per-concept frontmatter
read; the trade was accepted over caching a per-principal view, which would have to be invalidated
on every write. `auth.roles` is YAML-only — a rule is a structured object and the
`CARTOGRAPHER_TOKENS` string form cannot express it — so a deployment configured purely through
environment variables keeps whole-KB granularity. Principal IDs are derived from a token digest when
not set explicitly, replacing an earlier warning path that logged an 8-character plaintext token
prefix. Legacy behavior is preserved end to end: a token with neither scopes nor roles is still an
admin, and `Policy.Admin` bypasses the resolver entirely.
