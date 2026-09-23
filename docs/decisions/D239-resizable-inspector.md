---
topic: architecture
---

# D239 — The reading panel is resized by a splitter over the graph

**Decision.** The Atlas reading panel stays an overlay docked to the right of
the graph, and a splitter on its left edge sets its width. The canvas never
resizes: the camera's `occludedRight` follows the panel's rendered width
instead. The width is a per-browser preference in localStorage
(`cartographer.inspector.width`), clamped between 320px and what leaves 280px
of graph beside the rail. A window resize re-clamps what is drawn and never the
stored value. The splitter follows the WAI-ARIA window splitter pattern and is
a reusable component (`Splitter.tsx`).

**Why.** The panel was too narrow to read long concepts. Keeping the overlay
keeps the graph-first layout (D234, D235) and needs no change to the grid or
to the canvas' resize handling. Deriving `occludedRight` from the rendered width
also fixes a mismatch: the old constant ignored the 46% cap, so on a narrow
desktop window the camera over-compensated. The cost: a wide panel hides more
of a graph that is still drawn underneath it.

**Alternatives rejected.**
- A grid column that resizes the canvas: every drag would resize the WebGL
  canvas and refit the camera, and the graph would lose its full-bleed area.
- Keeping the width in the URL: it is not view state anyone shares.
- A splitter library: Pointer Events with pointer capture cover it without a
  new runtime dependency.
- Overwriting the stored width when a smaller window clamps it: the viewer's
  choice would shrink for good after one visit on a small screen.

**Consequences.** The panel's width lives in `App.tsx` state, measured against
`.shell__body` and `main`; the shell re-measures when it mounts, the rail folds
or the window resizes. Only a committed value (pointer up, a key, a reset) is
written to storage. `Splitter` sizes any panel from either edge, so a later
split (the planned artifacts list and detail) reuses it.
