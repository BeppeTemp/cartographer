---
topic: data-plane
---

# D1552 — A markdown href starting with `/` is a path from the KB root

**Decision.** `mdLinkTarget`, the one resolver behind the link graph, lint, `concept_move` and `concept_transfer`, resolves an href starting with a single `/` from the KB root (`/ops/b.md` is `ops/b`), not from the page's folder. `//host/x` is a URL. `RewriteLinks` keeps the bundle-relative form when it rewrites such a link on a move.

**Why.** `docs/conventions.md` and `docs/data-plane.md` already presented the form as the stable one, and the rebase repairs (`indexLinkForms`, `brokenLinkRebaseFixes`) already skipped it, but the resolver joined it to the page's folder: the link was a false `broken_link` that the write gate refuses, and missing from the graph. A rooted link does not change when its page moves, which is why a move must not turn it into a relative one.

**Alternatives rejected.** Declaring the form unsupported and removing it from the docs: pages already use it and it is the only form immune to a page move. Rewriting it to a relative link on a move: it throws away the property that made the author choose it.

**Consequences.** Every consumer of an href goes through `mdLinkTarget`; a new one must not re-implement the join. Asset links (`ExtractAssetLinks`) still skip rooted hrefs.
