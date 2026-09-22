---
topic: architecture
---

# D233 — The Atlas wears the Cartographer brand

**Decision.** The Atlas UI takes the brand of D232: the paper/ink/pine palette
with clay as a thin graphic mark, Georgia for concept titles and bodies, system
sans for everything operative, the Coordinate mark in the top bar and as
favicon. The brand's values are mapped onto the Atlas's existing token names in
`web/src/styles/tokens.css` rather than renaming them; hues 1–6 of the graph
wheel are the brand's `graphCategories`, and a test holds the two files
together. Neither theme is the default any more: the system decides until the
viewer picks one, and the resolved theme is applied before first paint by a
same-origin `theme-init.js`.

**Why.** The earlier "nautical" direction (navy, teal, amber, dark as the
signature) predates the brand and shared nothing with the README or the slides.
Keeping the token names made the change a values-only edit that
`tokens.test.ts` and `contrast.test.ts` keep guarding; every component kept
working untouched. The pre-paint script is an external file because the CSP
allows no inline script (`internal/webui/webui.go`), and a hash would have to
be recomputed on every edit.

Two defects surfaced on paper and were fixed where they bite. Sigma writes an
`rgba()` colour unpremultiplied into a premultiplied canvas, so a faded node is
*added* to the page: on dark it passed for transparency, on paper every faded
node and edge turned white. Fading is now a mix towards the canvas colour
(`encoding.fade`), opaque, identical in both themes. The border program draws
`transparent` as black, so a faded node's outline takes the faded fill.

**Alternatives rejected.**
- Renaming the tokens after the brand (`--paper`, `--pine`, …) — touches every
  component for no visual gain, and the role names (`--surface-0`, `--accent`)
  are what components should depend on.
- Clay as `--accent` — the brand reserves it for graphic marks; as small text
  it falls under 4.5:1. `--accent` is the brand's focus colour, `--clay` and
  `--clay-text` are separate tokens.
- An inline pre-paint script with a CSP hash — a second place to update on
  every edit, for one saved request.
- Dark as the default — the brand has no signature theme.

**Consequences.** A brand colour change lands in `docs/brand/` first and then
in `tokens.css`; the hue test fails until both agree. The mark is the master
SVG copied into `web/public/brand/`, never redrawn in CSS. `theme-init.js` and
`lib/theme.ts` share the storage key and the resolution rule, and a unit test
runs the script to hold them to it.
