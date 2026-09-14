---
topic: project-governance
---

# D202 — One file per decision, with a generated flat index

**Decision.** A decision record is one file, `docs/decisions/D<n>-<slug>.md`,
carrying a `topic:` field in its frontmatter. The list in `docs/decisions.md` is
generated from those files into a marker-delimited block and verified by
`internal/repodocs`. The ten thematic registers are gone. In code and in prose a
decision is referenced by the bare `D<n>`, resolved with
`ls docs/decisions/D<n>-*`; in markdown it is a real link, and every one of them
is checked.

**Why.** The registers made reading one decision cost the whole topic. 197
records lived in ten files totalling 483 KB; the largest, `sync-provisioning.md`,
was 101 KB, and the median register was around 55 KB. Answering "why is it like
this?" meant loading up to 101 KB to reach one paragraph. After the split the
median record is 2.1 KB and the largest is 7.3 KB — roughly 26× less for the
common case. It also removes a second level of routing: previously
`docs/decisions.md` → topic register → `#dNN` anchor, where the evidence on
progressive disclosure is that one routing level helps and a second one never
does and sometimes degrades accuracy.

The bare `D<n>` reference is deliberate. A path in a code comment breaks when a
title is reworded; `D47` does not, costs fewer tokens, and `ls docs/decisions/D47-*`
resolves it in one command.

**Alternatives rejected.**

- *Keep the registers and rely on anchors.* Anchors do not reduce what a reader
  loads: reaching `#d115` in a 101 KB file still pulls the file.
- *Keep the registers and split only the largest.* Two conventions in one
  directory, and the threshold would need defending at every commit.
- *Maintain the index by hand.* This is the failure mode observed in
  `backstage/backstage`, where adding an ADR means editing two configuration
  files and the two have already diverged. A hand-maintained index does not warn
  when it is wrong; it just stops being accurate, and then stops being read.
- *List every decision in the mkdocs `nav`.* Same divergence, plus ~200 sidebar
  entries. The nav points at the generated index and the records are marked
  `not_in_nav`.
- *One directory per topic.* That reintroduces the second routing level the
  split just removed, and topics change more often than decisions do.

**Consequences.** `make decisions-index` regenerates the block and CI fails when
it is stale, so it is not optional. `make decisions-next` gives the next number
on disk, but an open plan issue reserves its number before any file exists, so
the plan list still has to be consulted — that has not changed. Two plans adding
two decisions no longer conflict, because they no longer write to the same file;
the generated index is the one shared artefact, and it is regenerated rather than
merged. The thirteen original ADs stay in a single file
(`AD1-AD13-original-architecture.md`): they were written as one historical table,
never had individual bodies, and are all superseded — thirteen files each holding
one table row would add navigation without adding anything to read. The old
`#dNN` anchors are dead, and a test rejects any link that still uses one, because
an anchor-shaped reference is exactly the kind that survives a split silently.
`CHANGELOG.md` still names old register paths in released entries and is left
alone: a changelog is a record of what was said at the time.
