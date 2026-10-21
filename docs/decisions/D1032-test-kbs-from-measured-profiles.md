---
topic: architecture
---

# D1032 — The dev test KBs take the shape of measured real KBs, with invented content

**Decision.** The fleet `make dev` serves is eleven KBs: the 5,000-concept scale
KB and the 30-concept bare KB as before (D818), and nine KBs whose *shape* is a
table of numbers measured on a real multi-KB instance (`internal/testkbs/profiles.go`:
Maps and journals, expanded concepts and their satellites, concept sizes,
links, hubs, status mix, assets, artifacts, history) and whose *content* is
invented around a neutral theme per profile (`themes.go`: `seed-bank`,
`ferry-line`, …). The profile table is the only thing that enters the
repository from the measured instance; the measuring script stays with the
maintainer, and the real data is not needed to maintain the fleet. The output
stays a pure function of the spec and the day.

**Why.** The formulas of D818 gave every KB the same uniform shape, so none of
the Atlas bugs reported on real instances (a 490-entry journal, a concept with a
hundred satellites, pages past 90 KB, a hub with 170 backlinks, thousands of
commits on a few hundred concepts) reproduced on it. Numbers alone carry no
privacy cost: no name, title, path, type, vocabulary or text of the measured
instance is in the repository, and a test (`TestProfiledKBsUseOnlyTheirOwnVocabulary`)
fails the build if a generated page holds a word outside the package's own
tables, so a real name pasted in by mistake cannot reach a public revision
(D259). What it costs: the fleet is bigger to generate, and the history of the
largest profile (5,600 commits) is why it is written with `git fast-import`,
one process per KB, instead of one `git` invocation per operation.

**Alternatives rejected.**
- *Keep the formula-built KBs and add outliers by hand.* The extremes would be
  there but not the surroundings that make them bite (a big journal next to
  thin Maps, a status mix that is not 90 % `active`).
- *Anonymise a copy of the real KBs.* Anonymising text is a filter, and a filter
  leaks: one forgotten name in a public repository is permanent (D259). Numbers
  from a script that emits nothing else cannot.
- *Generate from a random distribution tuned by eye.* It would drift from what
  real instances look like; the table pins the target and a test checks each KB
  against it.
- *Keep the eight mid-size KBs (3,000 … 80 concepts).* The nine profiles cover
  that range with real shapes; the extra KBs only cost generation time.
- *Spawn `git` per operation (D818).* It does not scale to thousands of commits.

**Consequences.**
- Every profile row is a contract: a test (`profiles_test.go`) generates each KB
  and reads it back through `kb.Open`, so a generator change that moves a
  number fails by name. Where a row cannot be met literally it is met in the
  closest way and recorded here: satellites are the pages inside an expanded
  concept and come on top of the Maps' concept counts; a profile with assets
  but no expanded concept (a bare asset needs an owner) gets one expanded
  concept without satellites.
- The scale and bare KBs keep the formula path (`plan()`); the profiled ones
  have their own (`planProfile`); both share the file builders, the indexes, the
  artifacts and the local state.
- Findings are planted on `ferry-line` only (an island, broken links, the
  defects of `defects()`, the conflicts), so the other profiles open with no
  finding but `status_semantics`, which the status mix puts there by design.
- The privacy test reads every generated text file (concepts, Map contracts,
  text assets, `paths.yaml`, `instructions.md`) against the package's vocabulary
  tables plus a fixed list of English and scaffold words. It cannot catch a word
  pasted into the vocabulary tables themselves, which define what is allowed,
  and the tables of all themes are pooled, so one theme's words are allowed in
  another. Review changes to `themes.go` as a privacy matter (D259).
- A new Atlas state must be planted somewhere in the fleet and checked by
  `TestGeneratedKBShowsEveryPlantedState`.
