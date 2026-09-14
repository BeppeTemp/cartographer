---
topic: control-plane
---

# D149 — One link base and one concept resolver for lint

**Status: implemented (2026-08-28).** Closes #170.

**Context.** For the `index.md` of an expanded concept, no relative link form satisfied both
`lint` and the backlink graph, because the two resolved relative links against different
bases. `lint` derived it from the **ID** (`okf.IDToPath(id)`, which is just `id + ".md"`);
`buildLinkAdjacency` derived it from the **file** (`walkConceptPaths`'s `physicalPath`). For a
plain concept they coincide; for an expanded concept the ID is `map/c` (base `map/`) while the
body lives in `map/c/index.md` (base `map/c/`).

The consequence, measured on a KB with 52 expanded concepts: writing `[s](s.md)` produced
~300 false `broken_link`; writing `[s](c/s.md)` produced 54 falsely orphaned satellites. The
only escape was the wiki-link, which is root-relative and correct on both sides — at the cost
of the label.

A second, independent disagreement sat three lines below: the existence check was
`ReadRaw(okf.IDToPath(target))`, a plain `os.ReadFile` with no fallback, so a link to the
**canonical ID of an expanded concept** — the exact ID `concept_read` accepts — was reported
broken. `resolveConceptRelPath` already existed and its own doc comment already said every
read/write caller must resolve through it; `checkCuratedIndex` obeyed that (it calls
`ReadConcept`), the general pass did not.

Third: in a curated index, an expanded concept had to be linked `c.md`, because `c/index.md`
extracts as the ID `map/c/index`, which is not the candidate `map/c`. That is the **inverse**
of the inter-concept rule, with nothing to tell the two apart — 58 false `index_incomplete`
in the field.

**Decision.** `lint` resolves a body's relative links against the **physical path of the file
that contains them**, obtained from the KB via a new exported `kb.ConceptRelPath`.

The field report proposed the opposite alignment — making the graph use `IDToPath`. That is
rejected: it would make both sides agree on `c/s.md`, a link that inside `map/c/index.md`
points on disk at `map/c/c/s.md`, so the KB would stop rendering in GitHub, Obsidian or any
plain-markdown viewer. **A relative link must resolve the way the filesystem resolves it.**
The codebase already agreed in two of its three call sites: `expanded_as_category` and
`checkCuratedIndex` both pass the physical path, and the latter's comment says so explicitly.
Only the `broken_link` pass disagreed, with itself.

Also decided:

- **`Finding.Path` keeps its ID-derived value.** It is what every caller displays and sorts
  on; the physical path is a separate local variable used only as the extraction base.
- The existence check goes through `ReadConcept`, so `lint` and `concept_read` can no longer
  disagree about what a valid target is. The message text is unchanged, so only the *set* of
  targets considered present moves.
- A curated index accepts **both** forms, by inserting the parent ID of any `.../index`
  target into the comparison set. Deliberately not canonicalised in `ExtractLinks`: stripping
  a trailing `/index` there would silently rewrite graph edges and break
  `expanded_ambiguous`. A satellite literally named `index` cannot exist —
  `walkConceptPaths` treats `map/c/index.md` as the expanded concept's own body.

**Consequences.** `cartographer import` needed **no change**: it already computes the new
link base from `path.Dir(destPath)`, i.e. the physical base, so it stops generating findings
against its own output for free — under the report's proposal it would have needed one. That
closes the code half of the report's A4; its documentation half is a note in
`docs/configurator.md` §`cartographer import` saying the command rewrites links, because a
preprocessor otherwise fights it silently.

Behaviour change in lint output: a KB that adopted the `concept/satellite.md` workaround —
correct for the old lint, wrong on disk — will see new `broken_link` findings and must move
those links up one level or switch them to wiki-links.
