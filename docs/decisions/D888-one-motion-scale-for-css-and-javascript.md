---
topic: architecture
---

# D888 — One motion scale for the Atlas, CSS and JavaScript alike

**Decision.** Every animation of the Atlas UI is timed on the `--motion-*` and
`--ease-*` tokens of `web/src/styles/tokens.css`. What JavaScript animates (the
graph camera, the fade behind a selection, a count, the timer that ends the Map
contract's CSS slide) reads the same tokens through `motionMs()`
(`web/src/lib/theme.ts`), which returns 0 under reduced motion. Nothing the
reader starts lasts longer than `--motion-deliberate`. The graph's own
choreography (the bloom of a first layout, the morph between two views, the
settle, the growth replay) stays on its own constants: it is not a transition
of the interface. A folded group, or a button that folds something, shows one
chevron that turns.

**Why.** The CSS had kept to the scale for a long time while the JavaScript side
had grown its own numbers (600, 750, 450 ms), so a camera move outlasted the
panel sliding beside it and the cap stated in `tokens.css` held only for half
the UI. Reading the token at run time costs a `getComputedStyle` per move, not
per frame.

**Alternatives rejected.**
- Mirror the scale as TypeScript constants only: two sources of truth, and the
  reduced-motion collapse already written in CSS would need restating in JS.
- Make every graph motion obey the cap: the bloom and the morph carry the
  network's shape across a change; at 520 ms they read as a jump.
- A stylelint rule for the literals: a new toolchain for one check that the
  existing `tokens.test.ts` already runs in the `web` job.

**Consequences.** `tokens.test.ts` fails on a literal duration, delay or easing
in any `transition*`/`animation*` declaration outside `tokens.css` (the easter
egg excepted), and on `MOTION_MS` drifting from the CSS scale. A new JS motion
takes a token, not a number; a video export uses `MOTION_MS` so it does not
depend on the exporter's reduced-motion setting.
