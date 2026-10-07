---
topic: control-plane
---

# D339 — The reading panels share one page kit

**Decision.** Activity, Work and Health are built from one set of components
(`web/src/components/Page.tsx`) and one stylesheet (`web/src/styles/pages.css`):
a header whose title answers in a sentence with its count in bold and folds long
explanations behind a *?*; an open hero, with no box around it, that sums the
answer up — large figures, a chart where there is a series, filters as pills
and a Map bar; sections of dense rows without a box around them, with row actions shown on hover or focus; and a one-line,
positive note where a list would be empty. The pages share one width (1,360px),
mark rows with their Map's colour, and keep the previous answer on screen,
dimmed, while a new one loads. Artifacts is a catalog page of the same kit —
header, kind filters in the hero, a section of tiles per kind — and opens an
artifact in a panel beside it, the way the atlas opens a concept. No glow and no
decorative shadow: colour, glyphs and hairlines carry the emphasis.

**Why.** Each panel had grown its own header, width, list style and empty state:
880px columns beside full-width boards, bordered grey boxes, serif headings on
some pages and not others. The result read as several documents rather than one
application, and every new panel copied a different one. A kit makes the
consistent choice the cheap one and moves the decisions — widths, the band, the
row — to one place, where a change applies to every page.

**Alternatives rejected.**
- A component library: a dependency and its theming layer for a handful of
  pieces the token system already styles (`docs/conventions.md` keeps the
  runtime dependencies to what the UI cannot do without).
- Restyling each panel on its own: the drift that produced the problem.
- A band of cards, one per summary: tried first; a card sized by its row left
  empty space beside its neighbours, and boxes around a few numbers made the
  pages read heavier than their content.
- Folding the kit into `components.css`: pages and the graph shell change for
  different reasons, and one 2,700-line file hid which rules a page owned.
