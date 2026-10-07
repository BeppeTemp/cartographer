---
topic: data-plane
---

# D336 — The doctor is called until the Observatory is empty; a new artifact is not "never used"

**Decision.** `kb_status.conformance.doctor_suggested` counts every `lint`
finding the caller can see, not only the conformance checks, and
`conformance.lint_findings` reports that number. `artifact_unused` does not
report an artifact as *never activated* (or only catalogue-loaded) when the
KB's git history shows it added, or renamed into place, inside
`usage_stale_days`.

**Why.** A real KB sat at 18 info findings in the Observatory with
`doctor_suggested: false`: none of them was a conformance check, so nothing
ever called the kb-doctor session whose own definition of done is zero
(D306, D332). Seventeen were `artifact_unused` on skills committed four days
earlier against a 42-day threshold — a claim of disuse with no time to
observe it, and the one kind of noise that would make a zero target
unreachable.

**Alternatives rejected.**
- *Add the content checks to `conformanceChecks`*: that set is the
  structural drift the standard defines (D290, D318); the trigger and the
  severity counts are different questions, so only the trigger widens.
- *Measure the grace from the usage store's first report*: the store is
  local state an ephemeral data dir loses at every restart (D333), and a
  client's scan already covers its transcript window; the artifact's own
  age is what the check is missing.

**Consequences.** The doctor interval still decides when (D299): a KB is
not nagged daily for one info finding. Every finding already has a way out
(D313, D332), so a session can always reach zero. A KB that is not a git
repository keeps the previous behaviour.
