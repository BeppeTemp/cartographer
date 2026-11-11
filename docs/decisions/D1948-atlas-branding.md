---
topic: architecture
---

# D1948 — an operator's logo, favicon and one accent are approved data of a plugin's atlas side

**Decision.** A plugin's atlas side has a data-only family `branding/`: a declaration (`accent`, `company`, `logo.light`,
`logo.dark`, `favicon`) and the images it names. The product name is not data: no key renames or removes the word
"Cartographer". The plugin ships no CSS.

**Why.** Images are judged by their bytes, never by their extension, and an SVG holding any active content (script,
foreign objects, animation, event attributes, references outside the file, CSS escapes or loading functions) is refused,
because the route that serves it later is same origin. The declaration is read as strictly as a manifest (no unknown
key, no alias, one document) and `company` carries no markup or quote, so a consumer never depends on escaping.

**Alternatives rejected.** An operator-written stylesheet (it could restyle anything and carry `url()` requests). A name
override (the product name is code).

**Consequences.** A branding folder that parses is a `branding.Spec`; nothing loads it until the plugin glue lands.
Later changes extend this decision with the derivation of the accent and with how a server loads the family.
