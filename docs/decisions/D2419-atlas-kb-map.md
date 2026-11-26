---
topic: control-plane
---

# D2419 — The KB map is one per-request route over what the caller reads

**Decision.** `GET /kb-map` (preview tier) answers `{nodes, relations, links, truncated}` in one body: the nodes are exactly the rows of `GET /kbs` (one shared loop, `visibleKBs`), `relations` are the resolved declared dependencies between nodes, `links` the cross-KB link counts. A link is counted only when `crossStateFor` says `resolved` for the caller and the source page is visible to it: the one judge of "may be counted", the route adds only the node-set check. The body is computed per request, never cached, and carries an `ETag`. A KB's Atlas revision gets a `-x<12 hex>` suffix, a digest of the generations of the KBs it is cross-linked with (both directions) that the caller reads whole.

**Why.** The answer depends on the caller's rights on every KB, which the per-KB fingerprint of `serveUICached` cannot express; the inputs are the graph snapshots, read trusting a validation younger than the revision poll's window (3 s) like the revision itself, so a poll costs a computation over memory, no walk of the files and no transfer. Two layers in one body repeat the visibility walk once, not twice. The suffix makes an open page of A refresh when a linked KB changes; only KBs read whole count because `/revision` is whole-KB only, and a token that moved for a KB the caller cannot poll would disclose that it changed. The price: a caller who reads a linked KB only in part is not refreshed by it, and a poll computes the map each time (bounded by a 50,000-link budget, past which `truncated` is true).

**Alternatives rejected.**
- Two routes, one per layer: repeats the visibility walk.
- A body cache keyed by the visible set: the key would need the caller's rights on every KB, a fingerprint per pair; no cache is simpler and the work is bounded.
- Counting a link when the target KB is readable but the page is not checked: a count would reveal hidden pages; `crossStateFor` already decides this for the page graph and the lint.
- A suffix that names the linked KBs, or covers KBs read in part: it would disclose a change the caller may not see.

**Consequences.** A KB the caller cannot read is byte-identical to an absent one in `/kb-map` (pinned by a test). A relation edge comes only from a node the caller reads whole, because `settings.yaml` is a whole-KB file. A future change of the counting rule goes through `crossStateFor`, and both surfaces move together.
