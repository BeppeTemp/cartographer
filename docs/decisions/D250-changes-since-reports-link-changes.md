---
topic: control-plane
---

# D250 — changes_since reports link changes

**Decision.** `changes_since(links: true)` adds the structural delta of the
range: links added and removed, and the pages that became or stopped being
orphans. An edge belongs to its source file, so only the concept files changed
in the range are compared: their links at the base commit (the parent of the
oldest commit in the range; none for a root commit) against their links now (the
cached graph, D241). The default response is unchanged.

**Why.** A reviewer after an agent session needs to know what happened to the
structure, not only which pages changed. Because an edge changes only when its
source file changes, no graph has to be rebuilt at the old commit: the cost is
one `git show` per changed file.

**Alternatives rejected.**
- *Rebuilding the whole graph at the base commit*: exact, but the cost grows
  with the KB instead of with the range.
- *Reporting always*: it would change a response agents already parse, and costs
  git calls a caller who does not want it would pay anyway.

**Consequences.** Three approximations are documented and accepted:
- "now" is the files on disk, which equals HEAD whenever auto-commit is on;
- links at the base are extracted with no asset resolver, so an extensionless
  href counts as a concept link there;
- an edge that flips only because an extensionless asset appeared or vanished,
  with no change to its source file, is not reported.

A renamed file's old links are resolved against its path at the base, not its
current one. Only edges with both endpoints visible to the caller are
reported, and orphan status counts visible linkers only, so a hidden concept is
never disclosed (D226). Output is bounded: 500 edges (`truncated`), and past
1,000 changed concept files the delta is omitted with `links_note`.
