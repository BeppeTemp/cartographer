# Brand

Cartographer's identity: a contemporary atlas of knowledge — warm paper, ink and
pine green, calm editorial typography. Motion belongs to the knowledge graph, never
to the mark. The why behind the symbol is in
[D232](../decisions/D232-coordinate-is-the-mark-rilievo-a-motif.md).

## Symbol

**Coordinate** is the mark: an open C, an inner arc, a separate reference point.
**Rilievo** (soft contour lines around a core) is a background motif only — never a
second logo next to Coordinate.

| Asset | Use |
|---|---|
| `coordinate-pine.svg` / `coordinate-ink.svg` | Light surfaces |
| `coordinate-sage.svg` / `coordinate-ivory.svg` | Dark surfaces |
| `coordinate-micro-*.svg` | 16–24 px (no inner arc) |
| `coordinate-pine-256.png` / `coordinate-sage-256.png` | Tools that cannot take SVG |
| `favicon-light.ico` / `favicon-dark.ico` | 16/32/48 px favicons |
| `rilievo-*.svg` | Decorative motif |
| `banner-light.*` / `banner-dark.*` | README header, 1600×440 |

- Minimum size 32 px for the standard mark, 16 px (prefer 24) for micro.
- Clear space: at least ¼ of the symbol width on every side.
- Do not distort, tilt, shadow, gradient-fill, fill the hole, change the stroke
  weight or join the point to the body. Do not animate the mark.
- The name is written "Cartographer", set as live text next to the symbol — there
  is no custom wordmark.

## Palette

| Token | Light | Dark |
|---|---|---|
| background | `#F5F2EB` paper | `#191F1C` |
| surface | `#FCFAF6` ivory | `#222B25` |
| text | `#252824` ink | `#EEEDE5` |
| text-muted | `#646960` | `#AFB9AE` |
| accent | `#365D50` pine | `#91B9A0` sage |
| clay (graphic accent) | `#B8785D` | `#D6A68D` |

Clay is a graphic accent, not a small-text colour. Errors, warnings and success have
their own semantic tokens; brand green does not mean "OK". Graph categories use the
dedicated `graphCategories` palette so nodes never read as alerts.

The full set is in [`cartographer.css`](cartographer.css) (CSS custom properties,
`data-theme` override on top of `prefers-color-scheme`) and
[`cartographer.tokens.json`](cartographer.tokens.json) (same values plus graph
categories, spacing, radii and motion durations).

## Typography

- Editorial / headings: `Georgia, serif`.
- Interface: `system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`.
- Identifiers and code: `ui-monospace, SFMono-Regular, Consolas, monospace`.
- Slides: Georgia and Arial, for portability.

No font files are shipped.

## Voice

Short titles, concrete language, few superlatives. No unmeasured performance
promises, generic comparisons with other tools or unverified security claims. The
working tagline — "Give knowledge a sense of place." — is editorial copy, not a
technical claim.
