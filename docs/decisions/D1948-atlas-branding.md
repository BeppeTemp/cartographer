---
topic: architecture
---

# D1948 — an operator's logo, favicon and one accent are approved data of a plugin's atlas side, derived and verified by the server

**Decision.** A plugin's atlas side has a data-only family `branding/`: a declaration (`accent`, `company`, `logo.light`,
`logo.dark`, `favicon`) and the images it names. The product name is not data: no key renames or removes the word
"Cartographer". The plugin ships no CSS: the server derives the tokens that depend on the accent for the dark and the
light theme and renders them itself.

**Why.** Images are judged by their bytes, never by their extension, and an SVG holding any active content (script,
foreign objects, animation, event attributes, references outside the file, CSS escapes or loading functions) is refused,
because the route that serves it later is same origin. The declaration is read as strictly as a manifest (no unknown
key, no alias, one document) and `company` carries no markup or quote, so a consumer never depends on escaping.

The derivation is the only way to keep the contrast the Atlas already guarantees: the two themes have opposite
surfaces, so one colour rarely passes on both. `Derive` keeps hue and saturation and moves the lightness per theme until
the pairs `web/src/test/contrast.test.ts` audits hold (4.5:1 text and brand fills, 3:1 for the graphical object, and the
text on the hover shade `--primary-dim`), refuses a shift of more than 45 points and names the nearest colour that passes,
so the error is also the recommendation.

**Alternatives rejected.** An operator-written stylesheet (it could restyle anything and carry `url()` requests). A name
override (the product name is code). Refusing every accent that fails on either theme (it would refuse most brand
colours). Adjusting without a bound (a pale yellow would become an olive nobody chose). Several active brandings with a
precedence by pin order (the logo would depend on a map's iteration). A new server-side branding loader apart from the
atlas side's pins (the server already opens each pin once per side).

**Consequences.** A branding folder that parses is a `branding.Spec`. A
change to the surfaces in `tokens.css` fails `internal/branding`'s drift guard until the Go constants follow. The Go
contrast port and the TypeScript one are kept in step by a comment on each. A plugin's branding is
loaded by the server from the same `plugins` pins as its other sides: a branding-only plugin is an atlas pin. At most one active
plugin may provide a branding (two is a start error naming both, so no pin order decides the logo) and an accent that
cannot be shown stops the start naming the plugin and the key. Serving the stylesheet and the assets is the next
change's decision.
