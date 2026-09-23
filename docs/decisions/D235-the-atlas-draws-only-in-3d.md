---
topic: architecture
---

# D235 — The Atlas draws its graph only in 3D

**Decision.** The Atlas has one graph view, the living 3D network of D234, on
every screen width. The flat 2D view, its 2D/3D toggle, the draggable nodes and
the *Relax layout* control are gone. When WebGL is missing or the context is
lost, the graph area says so and the concept list, search and inspector keep
working, the same state as a browser without WebGL.

**Why.** After D234 the 2D view was the same scene laid flat. It added a second
set of gestures (drag to pan, drag a node), a second label policy (landmarks at
rest) and a toggle that asked the reader to choose between two pictures of one
graph, with the 2D view mainly serving as the fallback. The cost is that no
other graph picture exists when 3D cannot start. The list and search already
cover that case, and a browser that cannot run the 3D scene has no WebGL for
the 2D one either, since both used it.

**Alternatives rejected.**
- Keep 2D as a fallback only, with no toggle: the code stays for a case the
  no-WebGL state already covers.
- Keep 2D on narrow screens: the reader gets a different picture of the same
  graph when the window shrinks. Orbiting works with one finger.

**Consequences.** `LivingScene` has no mode: nodes are selected, never grabbed,
and every drag orbits. The remembered `cartographer.panel.3d` preference is
no longer read. Nothing clears it. A future flat view would be a new decision,
not a revert of this one.
