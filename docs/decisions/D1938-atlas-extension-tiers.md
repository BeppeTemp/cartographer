---
topic: architecture
---

# D1938 — The Atlas is extended by data, never by third-party JavaScript

**Decision.** A plugin extends the Atlas only through its atlas side, which is data: read-only views over public API routes drawn with a fixed set of renderers, and entries that open an app on its own origin in a new tab. The Content-Security-Policy stays the constant `script-src 'self'`, with no frame and no remote origin, and a test pins its exact directive set.

**Why.** The Atlas keeps its bearer token in browser storage, so any script served same-origin could read it. A declaration is approved by digest like every other data-only point, and a view is a presentation of a route, not a grant: the API still filters with the caller's principal. Loading judges the shape of a view's route; that it is a stable or preview GET route of the public API is checked by the server at start (`atlasext.Validate` over the route table), and a view that fails stops the start, like a pin that does not load. The list reaches the Atlas as one internal UI-API route, `GET /api/ui/v1/extensions`.

**Alternatives rejected.**
- A sandboxed iframe: it needs `frame-src` and a message channel to carry credentials.
- A script sandbox in the page: one escape reads the token.
- A reverse proxy to a plugin backend: it makes the Meridian a gateway to code it did not review.
- A `link` format in views: a URL that arrives as data would become clickable.

**Consequences.** `writeSecurityHeaders` takes no input and no plugin-declared string may reach a directive. An external app receives nothing from the Atlas and authenticates with its own credential. Someone who wants a different Atlas builds it on the public API.
