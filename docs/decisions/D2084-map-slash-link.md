---
topic: data-plane
---

# D2084 — `[[map/]]` stays broken, with a mechanical repair

**Decision.** A wiki-link written `[[map/]]` does not resolve: `wikiLinkTarget` still only strips `.md`. When `map` is a map with its index, `broken_link` carries a `rewrite_wiki_link` fix (`field` `map/`, `to` `map`), the canonical link to a map's index (D310), and `kb_repair broken_link` applies it, keeping alias and anchor. `[[nomap/]]` stays broken with no fix.

**Why.** An agent or an editor writes the trailing slash by habit; a doctor session rewrote eight by hand. The repair is mechanical: the map's index is the concept `map`.

**Alternatives rejected.** Resolving `[[map/]]` in the graph: a third spelling of one target for every reader, lint and `concept_move` to keep in step. Making `broken_link` auto-repair safe as a whole: its other fixes (rebase, self-link drop) are not run unattended, so the new one is applied by `kb_repair broken_link`, not by the heartbeat.

**Consequences.** `broken_link` accepts `rewrite_wiki_link`; a markdown link `[x](map/)` and a name that is no map get no fix.
