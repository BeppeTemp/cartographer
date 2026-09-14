---
topic: sync-provisioning
---

# D50 — Honest per-provider sync state: `unsupported` ≠ `needs_approval`, `InSync` requires zero differences

**Context.** After D48, an OpenCode sync showed "in-sync" and at the same time "8 needs approval": unsupported agents/
hooks ended up in `NeedsApproval` (but `--auto-trust` would never have unblocked them) and
`Diff.InSync` ignored artifacts left out.

**Decision.** `AppliedResult.Unsupported` (new field): `destRel == ""` → `Unsupported`, no longer
`NeedsApproval`. `provisioning.FilterForProvider` filters the manifest per provider before
Apply/diff/counts. `Diff.InSync` now requires identical revision **and** zero Added/Updated/
Removed.

**Discarded alternatives.** A dedicated badge for unsupported kinds in the counts; marking the
revision as not applied when NeedsApproval remain (it would have made every sync re-attempt
the apply with no progress).
Details: `docs/sync.md` §Agents and hooks.
