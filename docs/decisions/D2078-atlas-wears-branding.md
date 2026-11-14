---
topic: architecture
---

# D2078 — The Atlas wears the plugin branding

**Decision.** Meridian serves the active branding (D1948) to the Atlas: the light and dark logo and the company name, at
read-only UI routes. The Atlas shows the logo first in the top bar, then a thin divider, then the Cartographer mark and
word. The branding changes no colour and no favicon: the tab always shows Cartographer's own. Without a branding plugin nothing changes. The logos are served with an `ETag` and `Cache-Control: private, no-cache`, and a matching `If-None-Match` gets `304`: a logo changed by a plugin update shows on the next load, without downloading it again on every one.

**Accent removed.** By the maintainer's choice, before 0.29 was released, the `accent` key and everything derived from it
(the palette, the contrast checks, the `branding.css` route) are gone, with no migration: a `branding.yaml` that declares
`accent` is refused. This supersedes the accent part of D1948 (the logo and folder rules stand).

**Favicon removed.** By the maintainer's choice, in the same unreleased version, the `favicon` key, its route
(`/branding/favicon`) and the Atlas swapping the tab icon are gone, with no migration: a `branding.yaml` that declares
`favicon` is refused (`favicon: removed; the branding carries logos and a company name only`). `.ico` files are no longer
accepted. The Atlas removes a favicon link and ignores a favicon field in the cached branding a previous build left in
browser storage. This supersedes the favicon part of D1948.

**Why.** D1948 loaded and checked the branding but the Atlas did not show it, so an operator could not see the result of
the plugin they wrote. A logo is the part of a company's identity that is data and safe to show; recolouring
the Atlas was dropped as more than the branding should decide.

**Alternatives rejected.** Letting the plugin ship its own CSS or an accent: the Atlas keeps one palette whose contrast is audited.
Inlining the branding into `index.html` at serve time: the dev server (Vite) and the static Pages build would not see it.

**Consequences.** The word "Cartographer" stays code. The routes answer 404 without a branding, which the Atlas reads as
"no branding". A logo is served with the content type the spec judged it as and a strict CSP for SVG.
