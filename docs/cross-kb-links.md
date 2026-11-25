# Cross-KB links

A page can link a page of another knowledge base mounted on the same server. This page is the current state of that mechanism; the graph it extends is in `docs/data-plane.md` §Cross-links and the graph.

## Sibling KBs (`kb.Mounts`)
A base mounted by `serve` knows its siblings on the same server through `(*KB).Mounts()`: `Lookup` finds one by its mount name or by an old name that `kb_aliases` still maps (a mounted name beats an alias, D772; the map is read at start, a restart applies a change), `Canonical` gives the current name, `NamesFor` the current name and its aliases, and `(*KB).MountName()` the name it is mounted under. A single-base stdio server and a bare `kb.Open` have no mounts (`Mounts()` is nil, `MountName()` is empty). Nothing reads it yet: this is just the lookup mechanism.
