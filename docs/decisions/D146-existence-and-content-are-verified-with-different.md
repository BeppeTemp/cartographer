---
topic: client-configurator
---

# D146 — Existence and content are verified with different evidence

D139 gated its whole on-disk check behind a recorded `materialized_hash`: an entry without one
returned `unknown` before the filesystem was ever touched. `unknown` is not healable by design, and
`status` drops non-healable findings, so a managed artifact **deleted from disk** kept reporting
`in-sync` on any client whose lockfile predated D138 — the exact failure D139 was written to catch,
surviving in the population most likely to hit it.

The gate conflated two questions that need different evidence. *Did the bytes change?* cannot be
answered without a recorded hash. *Is the path still there?* can, by `stat` alone. `verifyArtifact`
now resolves the destination once (`managedDest`), stats it, and only then falls through to the hash
gate: a vanished artifact is `missing` regardless of what the lockfile knows about its content,
while one that is present but hashless stays `unknown`.

**The first-upgrade guarantee is untouched**, which is what made the original gate defensible: no
file that exists is ever rewritten on account of a missing hash. Restoring something that is *gone*
is not the mass rewrite that argument protects against — it writes only where there is nothing, and
it is what the operator already believes `sync` does.

Rejected: healing `unknown` outright (reintroduces the mass rewrite), and special-casing `status` to
surface unknowns as drift (would make every pre-D138 client exit 1 until it reconnects, and still
leave `sync` unable to restore the missing file). `doctor` keeps aggregating the remaining unknowns
into its one advisory line — with existence now covered, that line means what it says: content that
cannot be compared, not artifacts that might be absent.
