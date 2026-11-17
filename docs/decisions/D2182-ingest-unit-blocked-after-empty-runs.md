---
topic: control-plane
---

# D2182 — An ingest unit whose third consecutive run wrote nothing is blocked

**Decision.** When an `ingest` run ends with nothing written, the Surveyor counts the consecutive "released: nothing
was written" notes it wrote at the end of the unit's body; the third one patches `status: blocked` instead of `open`,
with a note saying the page may already be current, for a human to close or reopen. Any other Surveyor note (done, a
release for another reason, the blocked note) ends the run of notes, so the count is of consecutive empty runs only.

**Why.** A system poll can create units whose facts an earlier unit already wrote: the run writes nothing, the unit is
released and retried for ever at 20-60k tokens a run. The only brake was the in-memory skip TTL, lost on restart and
not shared between replicas. The unit body is the one record every replica and every restart sees.

**Alternatives rejected.** A counter in frontmatter: new state to keep in step with the notes. `skipped`: the next poll
recreates the unit. Letting the model close the unit: the prompt allows `blocked` only, and a model's text is never a
count.

**Consequences.** Only lines starting with the Surveyor's note prefix count. A reopened unit starts a new count, because
the blocked note does not carry the empty-run text. `claimable` already refuses `blocked`.
