---
topic: control-plane
---

# D165 — `search` scope is a literal prefix on both backends

**Status: implemented (2026-09-05).**

**Context.** The two search backends disagreed about what `scope` means. The in-memory index filters
with `strings.HasPrefix`; the SQLite backend filtered with `c.id LIKE scope || '%'` and no `ESCAPE`
clause. Concept IDs are file paths, where `_` is ordinary and common (`maps/cert_rotation`) and `%`
is legal — and to `LIKE`, `_` is a single-character wildcard. A scope of `maps_a/` therefore also
matched `mapsXa/`, and the doc comment promising "only concepts whose id starts with scope" was
wrong about its own code.

Nothing leaked: `handleSearch` re-filters SQL hits with `strings.HasPrefix` before returning them.
But that filter runs *after* `LIMIT`, so the out-of-scope rows were spent out of the caller's page
budget and then discarded — a scoped search silently returned fewer results than it should have,
with no signal that anything had been dropped. A whole page of over-matches returns an empty result
for a query that has hits.

**Decision.** `likePrefixPattern` escapes `\`, `%` and `_`, and the query pairs it with an explicit
`ESCAPE '\'`. `scope` is a literal id prefix on both backends, which is what the tool documented and
what the in-memory index always did.

**Rationale.** Escaping at the source was chosen over the two alternatives. Tightening the
tool-layer prefix re-filter treats the symptom and leaves the `LIMIT` interaction — the actual
user-visible defect — in place. Replacing `LIKE` with `substr(c.id, 1, ?) = ?` avoids metacharacters
entirely but gives up the `id`-prefix index range scan that makes a scoped search cheap, trading a
correctness bug for a performance one. The post-filter in `handleSearch` is deliberately kept: it is
now redundant, but it is the cheap assertion that keeps the two backends' semantics pinned together.

**Consequences.** A scoped search over IDs containing `_` or `%` returns the results it always
promised. Table-driven tests in `sqlindex_test.go` cover `_`, `%` and `\`.
