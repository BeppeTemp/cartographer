---
topic: data-plane
---

# D276 — A KB glossary expands search and flags forbidden terms

**Decision.** A KB may declare its terminology in one KB-root file,
`glossary.yaml`: a list of terms, each a required `canonical` with optional
`aliases` and `forbidden` lists, compared through `search.Fold`. It is handled
exactly like `paths.yaml` (D263) — written with `artifact_write` and validated
strictly, read tolerantly, malformed entries reported as `contract_malformed`,
never materialized on a client. `search` (and `graph_context`'s seeding) runs a
query once more for each variant obtained by replacing a whole-word canonical or
alias with each other member of its group, at most 8 queries including the
original, one level deep, merged by id at the best score and listed in
`expanded_to`; a miss is recorded only when every variant is empty. `lint`
reports a forbidden term used in prose outside code as `forbidden_term`
(warning, suppressible).

**Why.** Keyword search (D135) has no notion of synonyms, so an acronym and its
expansion, a product and its short name, or an old and a new name after a
rename are unrelated queries: the first misses, and `search_misses` (D247) shows
the miss with nothing an operator can do about it but rewrite pages. The write
side has the mirror problem: an old name keeps reappearing because nothing flags
it. One declared vocabulary fixes both. The cost is up to eight index passes for
an expanded query, and one more KB-root format to validate.

**Alternatives rejected.**
- *Rewrite the query to its canonical form.* It loses pages that only use the
  alias, and it makes the searched query differ from the typed one; running the
  original first and merging keeps today's result a subset of the new one.
- *Recursive or unbounded expansion.* A group with many aliases, or a query
  naming several groups, would multiply index passes; one level and a cap of 8
  bound the fan-out, and a group lists every member directly anyway.
- *Allow a string under two canonicals.* An expansion would then be ambiguous
  and a forbidden term could be another group's alias; the validator refuses it
  and the tolerant read keeps the first term.
- *Inject the glossary into client instruction files.* It would touch
  provisioning for a benefit the lint already delivers at write time.
- *Flag forbidden terms inside code.* A command, a config key or a quoted old
  name in a fence is not prose; the code masking the link graph uses (D150)
  excludes it, and `lint_ignore` covers a migration note that quotes the old
  name in prose.

**Consequences.** A KB without `glossary.yaml` searches and lints exactly as
before. Phrase matching is on word boundaries of the folded query, so an alias
never matches inside a longer word. On the FTS5 backend a term shorter than
three characters matches nothing alone (trigram tokenizer), so a two-letter
alias finds pages only through its longer variants. A future change to search
ranking must keep the merged result's order independent of which variant found
a hit first.
