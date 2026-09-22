---
topic: project-governance
---

# D232 — Coordinate is the mark, Rilievo a motif

**Decision.** Cartographer's symbol is *Coordinate* (open C, inner arc, separate
reference point). *Rilievo* (contour lines around a core) survives only as a
background motif. Brand assets, palette tokens and usage rules live in
`docs/brand/`, and the README opens with a light/dark banner instead of the ASCII
art.

**Why.** The brand study offered both symbols and left the choice open. Coordinate
carries the initial and a point of reference at once, keeps a distinct silhouette
down to the 16 px micro variant, and does not read as a generic target the way
concentric rings do. One mark means one thing to recognise; the cost is that
Rilievo's softer, cartographic feel is confined to backgrounds.

**Alternatives rejected.**
- Rilievo as the mark — concentric rings read as a target or a radar, and lose the
  irregular contour at favicon sizes.
- Both marks side by side — two logos in one header dilute recognition.
- Keeping the ASCII-art header — it cannot follow the reader's colour scheme and
  shares nothing with the future UI or the slides.

**Consequences.** New surfaces (UI, docs site, slides) take the mark and the
colours from `docs/brand/`; they do not redraw the mark in CSS. Swapping the symbol
later means replacing the `coordinate-*` files and the banners, not the tokens.
