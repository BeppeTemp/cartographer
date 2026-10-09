---
topic: data-plane
---

# D355 — Unattended repairs run to a per-concept fixpoint, in two stages

**Decision.** The background repair and repair-on-write repair each concept to a
fixpoint (re-evaluate the in-memory content, apply the allowed checks' fixes,
repeat; at most 8 passes) and write it once. Cross-concept checks run afterwards
through the per-check applier, and the concepts they touched go through the
fixpoint once more. A run is one commit with a quota of 500 concepts, and it also
fires shortly after a git pull that moved `HEAD`. Only checks whose registered
fix is `AutoRepairSafe` (D354) run unattended; `config.DefaultAutoRepair` is
exactly that set.

**Why.** Repairs depend on each other (rename a field, then its prose value
becomes visible, then it can be split), so one check per pass converged a KB over
as many days as the chain was long, 50 concepts at a time. Counting the quota in
concepts rather than in concept-times-checks is possible because one concept is
one write whatever the number of checks that touched it. The cost is one commit
per run instead of one per check: revert granularity is the run, which is
acceptable because every fix in it is deterministic and `repair_revert` still
reverts it as a unit.

**Alternatives rejected.**
- More runs per day: a tight timer on idle KBs for work that is one pull away.
- Ordering `DefaultAutoRepair` so dependencies come first: the order is a property
  of the content (a chain can be four links long), not of the list.
- Running `broken_link` and `reciprocal_link_item` unattended by default: they drop
  or rewrite links and are not `AutoRepairSafe` (D309); the plan's mention of them
  as default stage-2 checks yielded to the registry, which is the single source.
- A run per pull without a debounce: a burst of pulls would commit repair noise
  between every one of them; a trailing run after 10 minutes coalesces them.

**Consequences.** A fix that oscillates or never converges leaves its concept
untouched and is named in the log, so a new check needs a fix that is idempotent
on its own output. A partial fix (one that needs a person) does not hold back the
others on the same concept and is reported once. Stage 1 sees only what
`CheckConcept` computes after the first pass (graph-level findings such as
`duplicate_link` seed pass 0 from the caller's lint), so a chain that crosses into
a graph-level check is finished by the next run, not the same one. Repair-on-write
no longer restores a concept when one of its fixes is partial: the clean fixes are
written and the partial one stays as a finding. The `kb repair --apply` CLI plans
an artifact check but applies it only where `allow_artifact_write` is on.
