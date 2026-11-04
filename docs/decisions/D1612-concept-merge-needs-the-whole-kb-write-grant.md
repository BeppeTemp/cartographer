---
topic: transport-auth
---

# D1612 — concept_merge needs the whole-KB write grant

**Decision.** `concept_merge` is authorized like `concept_move` with link rewriting: only a token with a whole-KB write grant may call it. A role bounded by map, journal or type is refused with `needs_whole_kb`; a token with no grant on the KB still gets `not found`. The `rewritten` list in the response is filtered with the caller's view.

**Why.** The merge redirects every inbound link of the satellite across the whole KB, pages and curated indexes, so it writes outside the satellite and its parent. Authorizing only those two ids let a bounded role rewrite and commit pages it cannot see and learn their ids, against the perimeter model (D118, D243). The cost: a bounded role can no longer fold a satellite into its parent.

**Alternatives rejected.** Letting scoped roles merge and rewrite only the links they can see: it leaves dangling links elsewhere and needs a new link-rewrite contract. Filtering only the response: the writes outside the perimeter would remain.

**Consequences.** Any future tool that rewrites links outside its named ids must require the whole-KB write grant, or disable the rewrite for a bounded caller as `concept_move` does with `rewrite_links=false`. A bounded role that needs the consolidation asks a whole-KB writer.
