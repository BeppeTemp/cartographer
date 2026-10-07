---
topic: control-plane
---

# D338 — The reading panels share one page kit

**Decision.** Activity, Work and Health are built from one set of components
(`web/src/components/Page.tsx`) and one stylesheet (`web/src/styles/pages.css`):
a header whose title answers in a sentence with its count in bold and folds long
explanations behind a *?*; a band of cards side by side that sums the answer up
and filters it (stats, facet rows, a Map bar); sections of dense rows without a
box around them, with row actions shown on hover or focus; and a one-line,
positive note where a list would be empty. The pages share one width (1,360px),
mark rows with their Map's colour, and keep the previous answer on screen,
dimmed, while a new one loads. Artifacts keeps its two-pane layout but uses the
same header, counts and chips.

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
- Folding the kit into `components.css`: pages and the graph shell change for
  different reasons, and one 2,700-line file hid which rules a page owned.
