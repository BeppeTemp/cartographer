---
topic: architecture
---

# D281 — The reading panel stops growing at its prose measure

**Decision.** The reading panel's upper bound (D239) is the smaller of what
leaves 280px of graph beside the rail and what its content can use: the
concept's reading measure (`--reading-measure`, 68ch, the `.markdown`
max-width) plus the body's padding and an allowance for the border and a
scrollbar. `App.tsx` reads that width off a hidden probe styled with the
prose's own font, so the bound follows whatever font the browser resolved;
while unmeasured it falls back to the room alone. When the panel's width
changes under a selection, the graph reframes the selection in the strip that
is left, once the width holds still for 150ms, without the signals a new
selection sends.

**Why.** Past the measure a wider panel draws an empty band on its right and
takes graph the reader then cannot use: on a wide window the graph could
shrink to 280px behind a panel whose text stayed 68ch wide. Readers took it
for "text not responsive" and "graph unusable" (#436). Bounding the panel
bounds the cost D239 accepted — "a wide panel hides more of a graph that is
still drawn underneath it" — to the width that buys something. The cost:
tables and code blocks keep the prose's width even on a very wide window.

**Alternatives rejected.**
- Letting tables and code blocks use the extra width while prose keeps 68ch:
  it leaves the empty band on every concept with neither, and it keeps the
  graph squeezable to 280px for them too.
- A fixed pixel cap: 68ch is 544px in one serif and more in Georgia, so a
  constant is either too tight on one platform or leaves a band on another.
- Re-framing on every pointer move of a drag: each move restarts the camera
  tween, so the node chases the handle; the debounce frames it once it settles.

**Consequences.** The stored preference is never rewritten by the cap (D239's
rule for a window resize holds for it too): a width stored before this change
is drawn clamped. A change to the prose measure changes `--reading-measure`
in `tokens.css`, never a literal in `.markdown` or in the probe.
