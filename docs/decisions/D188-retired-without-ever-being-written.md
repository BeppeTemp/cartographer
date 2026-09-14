---
topic: project-governance
---

# D188 — Retired without ever being written

**Status: retired (2026-09-10). Never implemented, never specified.**

`D188` was reserved by a plan issue that was never filed. The audit that produced
`D185`–`D193` yielded eight plan issues (#240–#247) and skipped this number; five of
them named `D188` in their "Cross-plan order" section as a security prerequisite
landing first, and four declared themselves blocked on it. There is no issue, no
entry in any topic register, and no branch — nothing anywhere states what it was
meant to contain.

**Decision.** The number is **retired, not reused**. Nothing may be filed as `D188`.

**Rationale.** Two things had to be true and only one of them could be arranged.

The dangling prerequisite had to go: four approved plans were blocked on a
document that does not exist and could never be produced, so the references were
removed from #243, #244, #245, #246 and #247, each with a note recording why. That
unblocked the wave.

Reusing the free number for the next decision was the obvious tidy-up and is
rejected. Five issue bodies, now part of the repository's history, discuss "D188"
as a security item that lands first. A future `D188` about something else would
make those five paragraphs read as a description of it, and the reader would have
no way to tell. A hole in the sequence is a smaller defect than a number meaning
two things — and this project has just paid for the second kind: `D185` was
claimed by both a plan issue and a pull request, and the collision had to be
resolved by renumbering after the fact.

**Consequences.** The register skips from `D187` to `D189`, deliberately. If the
security concern the audit had in mind resurfaces, it is filed under a new number
with its own plan issue; it does not inherit this one.
