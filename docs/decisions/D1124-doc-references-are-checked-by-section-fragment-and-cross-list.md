---
topic: project-governance
---

# D1124 — A reference is also checked down to its section, its fragment and its cross-list

**Decision.** Extending D208 from paths and `D<n>` to what a pointer says inside
a page. In `make test`, `internal/repodocs` requires that every `<page>.md §Heading`
citation outside `docs/decisions/` names a heading of that page, that every
`#fragment` of a relative markdown link names a heading or an `<a id>` of its
target, and that the lists kept by hand agree: every `docs/` page is in
`docs/index.md` and in the `mkdocs.yml` nav, every cited `make` target and
repository path exists, and the `cartographer` usage table equals the dispatch
switch and the documented commands. `mkdocs.yml` also sets
`validation.links.anchors: warn`, so `--strict` fails the published site on a dead anchor.

**Why.** Renaming a heading, or mistyping a fragment, left a dangling pointer that
no compiler, link check or mkdocs run reported (about thirty were found at once),
and the cross-lists rotted the same way. Only the explicit `<page>.md §Heading`
form is read: a bare `§Heading` or a skill's own step number has no page to resolve
against.

**Alternatives rejected.**

- Heading text as a prefix only: many citations run on into prose, and a cut at a
  prose boundary cannot be guessed. The match is a word-prefix **in either direction**
  (the price: a short heading accepts any citation beginning with that word).
- One slug rule for fragments: the same file is read on GitHub and on the site and
  the two rules differ (`--` against `-`, `-1` against `_1`), so either is accepted.
- Relying on `mkdocs --strict` alone: it runs only on PRs touching `docs/**`, and
  reads neither `README.md`, `CONTRIBUTING.md`, `AGENTS.md` nor the skills.
- An allow-list pre-filled to make the first run green: lists start empty, an entry
  needs a reason and fails once it no longer applies (the D206 pattern); drift is
  fixed in the text.

**Consequences.** Renaming a cited heading fails `make test` with the citing
`file:line`. A raw `<title>` or `<textarea>` tag in hand-written prose of
`docs/decisions.md` hides the generated `<a id>` anchors from mkdocs's anchor
validation on a recent Python; the generator carries a comment about it. The new
test files cite examples in pieces, because the corpus includes them.
