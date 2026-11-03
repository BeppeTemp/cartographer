---
topic: deployment-release
---

# D1588 — A missing bundle asset is a 404, every bundle file has an ETag, the source hash follows git

**Decision.** The embedded UI's handler answers 404 for a path under `assets/` that the bundle does not hold; the SPA fallback to the shell is for client routes only. Every bundle file, the shell included, carries an `ETag` (a content hash computed at startup) and answers `If-None-Match` with 304. The provenance source hash covers the files git tracks under `web/`, and walks the directory only outside a git work tree.

**Why.** A tab opened before an upgrade asks for a hashed chunk the new build no longer has; the shell answering 200 made the lazy import fail with an HTML body, shown as a permanent WebGL error. `embed.FS` has a zero modification time, so unhashed files and the shell had no validator and were downloaded in full on every load. A hash over every file in `web/` made `TestBundleMatchesSource` fail on a Mac as soon as Finder wrote a `.DS_Store`, and made `make web` record a hash CI cannot reproduce. The cost: a new source file counts in the hash only once it is `git add`ed.

**Alternatives rejected.** A `Last-Modified` from the build time: it changes the response with no change in content. Keeping the walk and skipping known dotfiles: a list that never ends. Hashing tracked and untracked-not-ignored files: it depends on the machine's global ignore file.

**Consequences.** A new route family served from the bundle that is not under `assets/` still falls back to the shell. The hash changes when the algorithm does, so the committed `provenance.json` was re-recorded in this change. The Atlas says "reload the page" when the scene chunk cannot be imported, and reports WebGL as lost only when the context is.
