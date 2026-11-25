---
topic: client-configurator
---

# D2360 — approve judges known_kbs plus the bound KBs, tolerating a bound-only failure

**Decision.** `approve` (and `mcp probe`) pull `known_kbs` plus the KBs of the projections (narrowed to those receiving `--kb`), as `sync` does. A failure of a known KB stays fatal; a bound-only KB that cannot be pulled leaves out the projections that need it, with a warning that its collisions were not checked.

**Why.** `known_kbs` is refreshed by `sync`, not by `workspace bind`, so before the first sync a bound KB contributed nothing and the per-projection collision check passed, to fail only after the sync. Pulling the bound KBs makes approve and sync judge the same set; making their failure fatal would let one unmounted binding block the approval of every other KB.

**Alternatives rejected.** Only warning that a bound KB is not synced yet: leaves the collision unseen, the very blindness reported. Pulling bound KBs with any failure fatal: one stale binding blocks unrelated approvals, which `sync` avoids with `needsAny`.

**Consequences.** `--kb` on a KB neither known nor bound is still refused (#2347), now worded "not bound to any folder and not synced yet". A bound-only KB that fails is not judged at all, so a collision with it surfaces at sync.
