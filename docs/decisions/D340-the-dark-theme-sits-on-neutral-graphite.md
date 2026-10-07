---
topic: control-plane
---

# D340 — The dark theme sits on neutral graphite

**Decision.** The Atlas's dark surfaces are a neutral graphite (`--surface-0`
`#131416` up to `--surface-3` `#2b2c31`, borders `#2f3036`/`#7c7e86`) instead of
the brand's warm charcoal (`#1a1b19`…). Everything else in the palette stays the
brand's (D232, D233): the sage accent, the Map hues, the severities, the text
tones, and the light theme.

**Why.** The warm charcoal carries a faint olive cast. Under the sage accent and
mid-chroma Map hues the whole dark UI read as one muted green block: the colour
that should mark a selection, a Map or a state blended into the ground. A ground
with no hue of its own leaves all the colour to the things that mean something,
and the contrast audit (`test/contrast.test.ts`) still passes for every pair.

**Alternatives rejected.**
- Changing the accent or the whole palette: the brand's colours were not the
  problem, their ground was.
- A blue-slate ground: a second hue competing with the sage.
- A near-black ground with a glow: more contrast than the reading panels need,
  and a glow is decoration the brand kit avoids.
