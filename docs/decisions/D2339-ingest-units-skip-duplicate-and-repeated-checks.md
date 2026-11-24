---
topic: control-plane
---

# D2339 — Pages of shape `ingest-unit` are skipped by `duplicate_candidate` and `repeated_fact`, and an ISO timestamp's hour is not a glossary term

**Decision.** `kb_review` skips the pages whose frontmatter says `shape: ingest-unit` (the page the Surveyor writes for one observation) in the title pairing of `duplicate_candidate` and in `repeated_fact`, the way D1644 and D1406 skip journals: they are neither candidates nor owners, and the write-time twin of `repeated_fact` gives them no advice and does not count them as owners. `glossary_gap` masks the hour of an ISO timestamp (`T07` after a date) before extracting terms, for every page. The same-resource rule of `duplicate_candidate` is unchanged.

**Why.** Each unit records a distinct observation of a group at one revision, so units of one group carry the same title and the Surveyor's instruction text by construction. A KB fed by live sources got about 800 of 848 items from them (773 pairs, most of the repeated lines), which drowned the real work, and merging or linking observations is the wrong remedy. The hour of a timestamp (`T07`, `T23`) has the shape of an acronym and appears in any page that quotes a time. It costs the rare real duplicate or repeated line among units, which is no longer reported.

**Alternatives rejected.** Skipping only pairs that share `observed_group`: needs a field the check does not otherwise read, and the instruction text still repeats. Treating the unit instruction as template boilerplate (D314, D317): the text is not in a template. Masking timestamps only in units: the hour fragment is noise on any page.

**Consequences.** The skip keys on the `shape` value `ingest-unit`, written by `internal/surveyor/poll_system.go`: renaming the shape there needs the same change in `internal/lint`. A KB that wants units compared changes their shape.
