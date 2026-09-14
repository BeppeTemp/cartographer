---
topic: concurrency-git
---

# D164 — The search read lock spans the query, not the pointer load

**Status: implemented (2026-09-05).**

**Context.** `liveIndex` exists to make the in-memory keyword index safe for concurrent tool calls —
its own comment says it "synchronizes access to the underlying `*search.Index` instead of relying on
an unsynchronized `**search.Index`". It did not. `get()` took the read lock, returned the
`*search.Index`, and released it; both call sites then ran `SearchFiltered` on that pointer with no
lock held, while `concept_write` mutated the very same maps through `add()` under the write lock.

`search.Index` is two plain maps (`inverted`, `docLen`) with no synchronization of its own, so the
lock was protecting the pointer and nothing the pointer led to. Under HTTP, where a search and a
write land on different goroutines, this is a genuine data race whose usual symptom is a
`concurrent map read and map write` panic — the runtime's map corruption check, which kills the
process — rather than a merely stale result. `-race` reproduces it in under a second.

**Decision.** `get()` is replaced by `liveIndex.searchFiltered`, which holds the read lock for the
duration of the query. The index pointer never escapes the type again: every caller now asks
`liveIndex` to *perform* the search rather than to *hand over* the index, which is the only shape in
which the type's stated guarantee is enforceable rather than merely intended.

The `allow` predicate runs while the read lock is held, and the contract that it must not call back
into the same `liveIndex` is documented at the method. This is not pedantry: Go's `RWMutex` does not
allow recursive `RLock` when a writer is queued between the two acquisitions, so a callback that
took the same lock would deadlock under exactly the concurrent load this fix is about. The one real
predicate, `Visible`, touches only the auth policy and the KB.

**Rationale.** The alternative — giving `search.Index` its own internal mutex — was rejected because
it puts the lock in the wrong place. `liveIndex` already has to guard `meta` alongside the index and
already has to swap both atomically on a full reindex; a second lock inside `search.Index` would
make the two nest, and "index and metadata are consistent with each other" is a `liveIndex`
invariant that no lock inside `search.Index` can state. Keeping `search` a synchronization-free data
structure also keeps it honestly typed for its other callers.

**Consequences.** Searches and writes now serialise against each other for the duration of a query
rather than overlapping. The queries are in-memory ranking over a bounded candidate set, so the
contention is small and bounded — and it is the price of the operation being correct at all. A
`-race` regression test in `liveindex_test.go` runs concurrent `add` and `searchFiltered` and fails
against the previous code.
