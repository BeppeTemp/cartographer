---
topic: architecture
---

# D889 — The graph draws on demand, and a network too costly to tick rests its drift

**Decision.** The 3D Atlas draws a frame only when something moved or changed:
a simulation tick, a camera move (the reader's, its damping, the panorama), an
animation (bloom, morph, fades, signals) or a change the frame loop cannot see,
which the scene marks with `invalidate()` (new data or colours, a camera goal, a
frame listener, a tab coming back). Draws are at most about 60 a second
whatever the display's refresh. The live simulation's drift continues only
while one tick costs at most a quarter of a 60 Hz frame (`DRIFT_TICK_BUDGET_MS`,
4 ms, resumed below 3 ms); past it the network settles and stops ticking as
with motion off, while the panorama and the signals go on. The cost is measured
as the scene ticks, warm-up included, so the decision follows the graph on
screen and the machine it runs on.

**Why.** At 5,000 concepts one tick costs ~23 ms on an Apple M5: the always-on
drift of D234 took a full core and still could not hold 60 fps, and a static
graph with motion off was redrawn at the display's 144 Hz for an identical
picture (#861's idle measurement). The breathing network stays what D234 made
it on the graphs where it is affordable; on the large ones the reader keeps a
turning, responsive picture instead of a hot machine.

**Alternatives rejected.**
- Lower the tick rate with the graph's size: a 23 ms tick a few times a second
  is a visible hitch in every rotating frame, and the drift slows to nothing.
- A fixed node-count threshold: the cost depends on the machine and on the
  links as much as on the count; measuring it answers both.
- Breathing as a draw-time offset with the simulation cooled: cheaper, but it
  is a second motion model to keep in step with the physics for one effect.

**Consequences.** A new way to change the picture from outside the frame loop
must call `invalidate()`, or it shows only with the next movement. The video
export (`renderFrame`) always draws and never adapts its drift, so its frames
do not depend on the exporting machine. `sustainsDrift` is the one place the
budget lives.
