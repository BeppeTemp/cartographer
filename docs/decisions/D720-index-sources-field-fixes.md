---
topic: control-plane
---

# D720 — Indexes, Sources and proposals hold up on a large imported KB

**Decision.**
- A markdown link label may hold one level of square brackets (`[Issue — [RFC] x](x.md)`). The
  link graph, `index_incomplete` and the lint link checks parse it with the same pattern.
- A page cited in another page's frontmatter `provenance` is not `orphan`: being cited is a way
  in.
- A field whose values are identifiers (every value URL-shaped, or each of at least three
  carriers holding its own value) is never proposed as a closed vocabulary, neither by
  `missing_value_contract` nor by template proposals (#686).
- `template_proposal` makes one proposal per template slug, built from every page that would bind
  to it across the maps that would declare it. A section or optional field enters only when at
  least half of those pages, and at least two, carry it. A value list enters only when every map
  gives the same one. A proposal spanning several maps carries one `map_updates` entry per map
  (#687).
- `index_get` with `with_hash` always returns `content`.
- `concept_move` adds the plain entry `- [title](path)` under `## Moved here`, with no
  `: moved from` suffix.
- A `source_register` duplicate whose locator differs says so (`locator_mismatch`,
  `existing_locator`).
- `source_list` returns `cited_by_ids` next to the `cited_by` count.

**Why.** A KB built by bulk ingest (hundreds of pages, one Source per page) hit each of these at
scale. Issue titles with `[RFC]` became dozens of false "missing" index entries. Every Source
cited only in `provenance` was an orphan. URL fields were proposed as enums. One template slug got
six conflicting files. A script reading `index_get` broke once the index passed 60 KB. A move left
45 suffixes to clean up. Two pages with identical text silently shared one Source. Each case was
a rule written for a hand-built KB, and the scale made it visible.

**Alternatives rejected.**
- Escaping brackets when index entries are written: it would not fix pages written by hand or
  imported, and the parser is the single point every reader goes through.
- Deduping Sources by (sha256, locator): two locators with the same content are often the same
  document (a mirror, a redirect). Reporting the mismatch leaves the choice to the agent.
- An `index_note` option on `concept_move`: an option nobody would set to keep the suffix. The
  move is already in the git log.
- Per-map template proposals with a conflict warning: a template is one file per KB, so the
  proposal has to be one too.
- Returning an error from `index_get` over the size guard with `with_hash`: the caller asked for
  the content in order to patch it.

**Consequences.** Every link reader must use the bracket-aware pattern (`internal/kb/graph.go`, `cmd/cartographer/import.go`, `web/src/lib/linksection.ts`,
`internal/lint`): a new copy of the old regex brings the false findings back. The `kb-doctor`
skill applies every `map_updates` entry of a multi-map proposal. Scoped lint reads every concept's
frontmatter once when an orphan candidate appears, which costs one KB walk on that write.
`identifierValued` is the single home of the "this is an identifier" rule.
