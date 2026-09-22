---
topic: architecture
---

# D234 — The 3D atlas is a living elastic network

**Decision.** On a wide screen the Atlas opens in 3D. The 3D view is a
force-directed network in three dimensions with one writer of coordinates, the
`d3-force-3d` simulation inside `3d-force-graph`: springs on the links,
Barnes–Hut repulsion, weak per-axis gravity in place of d3's centring force,
damping, and a small deterministic drift. Dragging a node pins it on the camera
plane and reheats the simulation, so its neighbours follow and the motion
travels through its component; on release it settles. At rest the network
breathes and the panorama turns at ~1°/s, only in the overview after 10 s of
quiet. A selection eases the camera onto the node's neighbourhood beside the
inspector, follows it while selected, names it, and sends three finite waves of
signals along its links in their data direction. A remembered *Motion* toggle
and `prefers-reduced-motion` stop everything autonomous; the reader's own
gestures always work. The 2D view is unchanged and still (D227).

**Why.** The brand kit asks for a navigator whose graph is alive and reacts to
touch — dragging a node must move the network, not rotate a rigid group. D227
records how that went wrong in 2D: an idle animation and a layout worker wrote
the same coordinates, and the graph shivered at rest and flew apart on a drag.
Here every motion is a force in one simulation, so there is nothing to fight.
d3's `forceCenter` was replaced because it translates every node by the same
offset: a drag in one component slid every other component with it. The drift
is a force, not a tween, and a headless test bounds its per-frame motion so it
never reads as jitter. `3d-force-graph` hides `d3AlphaTarget` and
`resetCountdown` (it drives them for drags); the live alpha floor reaches them
on the inner `three-forcegraph` object, and degrades to "settles and stops" if
a future version moves it.

**Alternatives rejected.**
- Keeping 2D as the default and 3D as an extra — the brand asks for the 3D
  navigator; 2D stays one click away and takes over on a lost WebGL context.
- A bespoke simulation (as the kit's Studio 03 prototype) — O(n²) repulsion,
  and a second physics engine next to the one `3d-force-graph` already runs.
- A layout worker — the CSP refuses `blob:` workers (D227), the main-thread
  simulation meets the budget at the sizes the API serves, and a worker would
  bring back two writers of coordinates.
- Idle rotation of a rigid group, or a per-node tween, as "life" — rejected by
  the brief and by D227.
- `zoomToFit` for framing — it runs before `graphData()` is applied (the data
  is applied asynchronously), so framing and focus are computed from settled
  positions, on the first engine tick after each data change.

**Consequences.** Physics constants live in `web/src/lib/graph3d/physics.ts`
and are held by `src/test/physics.test.ts` (hub drag propagates and fades with
distance, a leaf moves less than a hub, an unlinked component is nudged by
repulsion but not towed, release never teleports, drift has no jitter, paused
motion dies out). The view draws up to 5,000 concepts, the graph API's ceiling;
past that it says so. Without WebGL neither view can draw — Sigma needs it as
much as three.js — so the shell checks once up front and shows a named state
with the list, search and inspector intact. A future LOD/aggregated 3D for
larger KBs needs a server-side change first (the API caps the snapshot).
