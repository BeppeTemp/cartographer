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
colours). Adjusting without a bound (a pale yellow would become an olive nobody chose).

**Consequences.** A branding folder that parses is a `branding.Spec`; nothing loads it until the plugin glue lands. A
change to the surfaces in `tokens.css` fails `internal/branding`'s drift guard until the Go constants follow. The Go
contrast port and the TypeScript one are kept in step by a comment on each. A later change extends this decision with how
a server loads the family.
