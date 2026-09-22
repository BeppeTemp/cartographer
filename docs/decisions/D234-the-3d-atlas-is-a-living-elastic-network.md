---
topic: architecture
---

# D234 — The 3D atlas is a living elastic network

**Decision.** On a wide screen the Atlas opens in 3D. The 3D view is a
force-directed network in three dimensions, drawn with three.js directly
(`web/src/lib/graph3d/scene.ts`) the way the brand kit's Studio 03 draws it —
small unlit nodes in one instanced mesh, hairline links in one line set, a
wireframe ring on the selection, signals as points. One writer of coordinates,
the `d3-force-3d` simulation the scene runs: springs on the links,
Barnes–Hut repulsion, weak per-axis gravity in place of d3's centring force,
damping, and a small deterministic drift. At rest the network
breathes and the panorama turns at ~1°/s, only in the overview after 10 s of
quiet. A selection eases the camera onto the node's neighbourhood beside the
inspector, follows it while selected, names it, and sends three finite waves of
signals along its links in their data direction. A remembered *Motion* toggle
and `prefers-reduced-motion` stop everything autonomous; the reader's own
gestures always work. The 2D view is unchanged and still (D227).

**Why.** The brand kit asks for a navigator whose graph is alive — nodes moving
locally under the physics, not a rigid group turning. D227
records how that went wrong in 2D: an idle animation and a layout worker wrote
the same coordinates, and the graph shivered at rest and flew apart on a drag.
Here every motion is a force in one simulation, so there is nothing to fight.
d3's `forceCenter` was replaced because it translates every node by the same
offset: any perturbation of one component slid every other one with it. The drift
is a force, not a tween, and a headless test bounds its per-frame motion so it
never reads as jitter.

The first version drew with `3d-force-graph`. It was replaced after review:
its lit, full-size spheres made the graph read as a toy next to the kit's
prototype, it draws one mesh per node and one line per link (2,000 nodes
dragged at 30 fps), it applies data asynchronously (framing and labels had to
wait for its first engine tick), and it hides `d3AlphaTarget`, which live mode
needs. Owning the scene removed all four: one draw call per kind of thing,
60 fps at 2,000 nodes while dragging, and the bundle lost ~225 KiB.

**Amended: nodes are selected, not dragged.** The first version let the reader
grab a node and pull the network. The maintainer dropped it to save resources:
a drag reheated the whole simulation on every pointer move and needed a
gesture arbitration against the orbit (capture-phase claim, second-contact
hand-over, synthetic releases). Now every drag orbits and a click selects; the
graph stays alive through the drift, the panorama and the signals.

**Alternatives rejected.**
- Keeping 2D as the default and 3D as an extra — the brand asks for the 3D
  navigator; 2D stays one click away and takes over on a lost WebGL context.
- `3d-force-graph` (the first implementation) — see above.
- The prototype's own simulation — O(n²) repulsion; `d3-force-3d`'s Barnes–Hut
  scales and is what the physics tests measure.
- A layout worker — the CSP refuses `blob:` workers (D227), the main-thread
  simulation meets the budget at the sizes the API serves, and a worker would
  bring back two writers of coordinates.
- Idle rotation of a rigid group, or a per-node tween, as "life" — rejected by
  the brief and by D227.

**Consequences.** Physics constants live in `web/src/lib/graph3d/physics.ts`
and are held by `src/test/physics.test.ts` (deterministic seeding, gravity in
place of centring, drift without jitter, paused motion dies out, NaN repair). The view draws up to 5,000 concepts, the graph API's ceiling;
past that it says so. Without WebGL neither view can draw — Sigma needs it as
much as three.js — so the shell checks once up front and shows a named state
with the list, search and inspector intact. A future LOD/aggregated 3D for
larger KBs needs a server-side change first (the API caps the snapshot).
