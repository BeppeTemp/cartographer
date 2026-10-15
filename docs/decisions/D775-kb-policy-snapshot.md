---
topic: data-plane
---

# D775 — A KB's policy is one immutable snapshot replaced whole, read through one accessor

**Decision.** The per-KB policy (artifact and template writes, write gate, auto-repair, repair on
write, doctor cadence and budget, usage threshold) lives in a `kb.Policy` value held by the KB in an
`atomic.Pointer`. `KB.SetPolicy` stores a copy; `KB.Policy()` returns a copy. A reader takes one
snapshot per call and uses it throughout.

**Why.** D746 made the settings refresh run at runtime, but the policy stayed a set of plain
`kb.KB` fields assigned one by one while kb_status, lint and the write tools read them without a
lock: a data race, and a reader could see half of an old policy and half of a new one. A mutex
around each field would not fix the second problem; replacing the whole value does both.

**Alternatives rejected.** An `RWMutex` over the fields: every reader must remember to lock, and the
compiler cannot find the ones that do not. Keeping the refresh inside the git lock only: kb_status
and the Atlas read without it.

**Consequences.** The policy fields are gone from `kb.KB`, so a new reader cannot read one without
going through `Policy()`. Slices in a snapshot are copies; an in-place edit of a returned slice
never reaches the KB. Tests change a field with `p := k.Policy(); p.X = ...; k.SetPolicy(p)`.
`internal/config`'s `KBSpec` keeps its own fields of the same names: they are the configuration, not
the resolved policy.
