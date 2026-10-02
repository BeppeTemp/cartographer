---
topic: data-plane
---

# D304 — Map titles are reviewed as one set, and renamed through map_update

**Decision.** `kb_review` emits one `map_naming` item when a KB's map titles
(three or more) mix a subtitle with none, or Title Case with sentence case. It
names every map as `<map>/_map` and lists each folder and title as evidence.
`map_update` takes a `title`, which renames the map in `_map.md` and `index.md`,
H1 included, and never touches the folder.

**Why.** On a real KB the rail listed maps named in two languages, with and
without a subtitle, in two capitalisations. That is noise every reader pays for,
and nothing reported it. Nor could an agent fix it: a map's title lives in four
places (`_map.md` and `index.md`, frontmatter and H1), and `map_update` changed
only the contract. The server measures the two properties it can measure in any
language: the shape and the capitalisation of a word. Whether titles share one
language, or match their folder, is a judgement the item hands to the agent by
listing every title. The cost is one more review kind, and a pseudo-ID for a map
that is not a concept.

**Alternatives rejected.**
- *One item per map in the minority shape*: renaming is one scheme for the whole
  set. Split into items, the operator would agree a scheme once per map.
- *Detecting language mix or folder mismatch*: no generic test separates a folder
  id whose title is a translation from one whose title is a different subject. A word-overlap
  test would flag every KB whose folders are English ids and whose titles are not.
- *Renaming the folder too*: every link and every scope rule names the folder.
  A title is free to change, a folder is an ID.

**Consequences.** `map_naming` is whole-graph, so a caller restricted to part of
the KB never sees other maps' titles. It is dismissed by `lint_ignore` in a named
map's `_map.md`, which the review reads for that purpose alone. The `kb-doctor`
skill proposes the scheme, and renames only after the operator agrees.
