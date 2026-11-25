# Cross-KB links

A page can link a page of another knowledge base mounted on the same server. This page is the current state of that mechanism; the graph it extends is in `docs/data-plane.md` §Cross-links and the graph.

## Sibling KBs (`kb.Mounts`)
A base mounted by `serve` knows its siblings on the same server through `(*KB).Mounts()`: `Lookup` finds one by its mount name or by an old name that `kb_aliases` still maps (a mounted name beats an alias, D772; the map is read at start, a restart applies a change), `Canonical` gives the current name, `NamesFor` the current name and its aliases, and `(*KB).MountName()` the name it is mounted under. A single-base stdio server and a bare `kb.Open` have no mounts (`Mounts()` is nil, `MountName()` is empty). Nothing reads it yet: this is just the lookup mechanism.

## Syntax, extraction and storage

`[[kb:<name>/<id>]]` (optional `|label`, `#section` ignored) links a page of another KB mounted on the same server; `<name>` is the mount name or an alias. The grammar reserves `[[kb:<name>@<server>/<id>]]`: it is parsed and kept (`CrossLink.Server`) and resolves nowhere. A malformed one (bad name or server segment, empty or non-canonical id) is kept with `Malformed` set, never a parse error. Only the wiki-link form counts: a wiki-link or markdown target starting with `kb:` is never an internal link (0.29.2), and a markdown `kb:` href is plain text. Links are extracted from concept bodies only, code spans and fences masked, and cached with the graph entry (`CrossLinks`, `CrossLinkedKBs`). Cross edges live beside the in-KB adjacency, not in it, so PageRank, `graph_context` and `search` never see them.

## Resolution

Resolution is one-server and reads only the other KB's graph snapshot. `CrossTarget` asks whether the page exists there, and `CrossIncoming` which of its pages link here (by this KB's name or an alias). A KB with no mounts, an unmounted name, a malformed link or a server qualifier resolves nowhere, and a KB whose graph cannot be validated is unknown, never missing or absent (`CrossTarget.Err`, an error from `CrossIncoming`, an `unknown:` digest). Other KBs are read from their graph snapshot validated within 3 seconds, so a caller that asks once per page does not walk their files each time. `CrossInboundDigest` is a memoized sha of the incoming links, keyed on the other KBs' graph generations, so a cached result can be invalidated when a link toward this KB appears or goes. Nothing reads these yet.
