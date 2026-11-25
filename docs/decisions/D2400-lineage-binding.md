---
topic: sync-provisioning
---

# D2400 — A binding carries the KBs its KBs depend on, computed from the winning binding's own KBs

**Decision.** The set a folder receives is the winning binding's own KBs, plus the stored `closure` of each of them, minus the binding's `exclude`; `no_lineage` follows nothing. This amends D1019 and the deepest-wins rule (D737): the winning binding is still chosen across explicit and derived ones as before, and lineage is then taken of **its own KBs only**. Ancestor bindings are never merged in, and a lineage KB is not a seed of anything (its `repos:` derive nothing, its closure is already inside its seed's). Lineage is stored per seed KB in the derived state (`workspace-derived.json`, `lineage`), never in `.cartographer.yaml`, and the overlay is applied at read time in `effectiveWorkspaces`, the one place that combines the sets, so the bridge, `approve` and the projections cannot disagree. `WorkspaceBinding.KBs` stays the effective set and `OwnKBs()` is the written one. The opt-out (`no_lineage`, `exclude`) lives on the explicit binding, decided on this machine, never in the KB.

**Why.** A client KB's repositories always need its core KB; binding both by hand in every repository, or losing the core KB in a derived repository under an explicitly bound folder (a derived binding narrows the set), is the cost this removes. Storing the closure keeps the bridge and `approve`, which never pull, consistent with `sync`; the cost is that a relation change reaches them with the next `sync`. Stored data is untrusted: on load only the name rules and a cap of 64 names per KB apply, so a hostile or damaged file costs its own entry; whether a name is mounted is a test for what the server newly sends, never for what is stored.

**Alternatives rejected.**

- *Union with the ancestors' sets.* It would widen a folder by what some folder above happens to bind, break `--no-kb` as a local opt-out and the fail-closed meaning of a binding (D193).
- *Storing the effective set in the binding.* A relation edited on the server would rewrite the user's file, and an excluded KB could not be told from a hand-bound one.
- *The opt-out in the KB.* The person working in a folder decides what it receives, not the KB that declares the relation.

**Consequences.** `exclude` removes only KBs that came by lineage and only the named ones. A rebind replaces the binding whole, so an option not repeated is off. A derived binding has no keys: opting out there is an explicit binding on its path.

**Pulling and storing.** `sync` stores the lineage of every seed KB (the written list of each provider with an explicit one, the KBs of explicit workspace bindings, the KBs of derived bindings) from the `relations` of the pulls it already makes. An absent or failed answer never means "none": a failed pull, a seed another provider's run left alone, an answer with no `relations` (an older server) and `relations_unavailable` (a fresh clone with no `origin/HEAD` yet) each keep the stored entry, and only an explicit empty `relations` removes it. A lineage KB the server could not mount (`skipped_kbs`) and that left the closure is held, not unbound: the seed's entry stays, so the KB stays in the projection and its failed pull keeps every holder's state (#350); this does not use `HeldKBs`, which `connect-all-dynamic` introduces for another case.

