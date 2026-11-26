---
topic: project-governance
amends: D1877
---

# D2414 — A train takes every queued unit whose pre-check is not red, and the lander rebuilds the bundle of every web change

**Decision.** The lander composes a train from every landable unit whose pre-check is green, gives no verdict or is still running; only a red unit is gated alone. The collection window lasts 15 minutes (`LAND_WINDOW` 900 s). The pre-check's replay keeps `main`'s Atlas bundle when the replay conflicts in `internal/webui/dist` only, says `bundle=stale`, and its jobs then skip `TestBundleMatchesSource` and `web-check`'s provenance diff while still building the bundle and checking its budget. The lander builds the bundle again for every picked commit that touches `web/`, not only after a conflict, and `make web` refuses untracked files under `web/`.

**Why.** Gate time does not depend on train size: a train of one costs what a train of eight does. Of 336 trains between 11-05 and 11-26, 223 held one change, and the lander's log says "pre-check still running and nothing else is ready: gated alone" 108 times: a pre-check takes 8 minutes median and 17 at p90, so a change queued in the second half of a gate missed the next train. 12 of 16 red pre-checks opened were a replay conflict in the committed bundle only, each costing its unit a gate of its own. A replay of the real queue times puts the change at 299 to 230 trains and 33 to 28 minutes of mean wait. The cost: a change red only on the replayed tree can redden a shared train and cost a bisection; the review's green `make check-hosted` on the same tree, the window and the bisection bound it.

**Alternatives rejected.**
- Wait for every pre-check before composing: the lander idles 8 to 17 minutes for a verdict the train gate gives anyway.
- A wider window without taking running units: before this change the window only delayed trains of one; in the replay it caught nothing.
- Rebuild the bundle only after a conflict (D2040): a bundle built before a new file was `git add`ed hashes other sources, and reached the gate red.
- Keep the change's bundle side in the pre-check replay: either side is stale; `main`'s is the one the lander starts from.

**Consequences.** A pre-check is now a filter for red only: a unit is isolated when its verdict is red before the compose. `TestBundleMatchesSource` skips when `READY_CHECK_BUNDLE=stale`, which only `ready-check.yml` sets. Vite output is not byte-stable across Node patch versions; the lander already accepted that when it rescued a conflict, and now accepts it for every web change.
