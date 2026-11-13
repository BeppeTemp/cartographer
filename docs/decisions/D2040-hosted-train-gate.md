---
topic: project-governance
---

# D2040 — The gate of a maintainer's change runs on GitHub-hosted runners, every leg main runs

**Decision.** A maintainer's checks and the lander's train gate run on GitHub-hosted runners: `make check-hosted` is an agent's check, and `SIGNOFF_HOSTED=1` makes `make signoff` (so the lander) dispatch `ci.yml` on the branch and sign off only when every job of `HOSTED_JOBS` is green — `gate` plus every leg `main-status` watches, Windows and macOS included. A shared build host serves only targeted runs and Windows debugging. An implementer iterates on the targeted tests of what it touched; a chain of changes is checked and reviewed once, on the plan's tip. The lander resolves a cherry-pick whose only conflict is the committed Atlas bundle by building it again from the merged sources.

**Why.** D1610 moved the gate to the maintainer's machine because "the account gets one or two hosted runners at a time". Measured on this repository (public, so hosted minutes are free) that no longer holds: a dispatched `ci.yml` started its 20 jobs within 4 seconds and finished the whole matrix in about 10 minutes. The build host, by contrast, is one slot: every agent's `make check`, every review round's re-check, every train gate, retry and bisection probe queued on one lock. On one morning 58 runs spent 199 minutes waiting for 341 minutes of work, with waits of up to 32 minutes, while a dozen agents worked in parallel. The local gate also never ran macOS, and its Windows leg only the affected packages, so a Windows- or macOS-only break was first seen on `main` (42 of 152 push runs red in one week), and every red `main` became a follow-up change in the same queue. Of 59 trains in a week, 27 changes were rejected for conflicts, many only in the bundle every frontend change rebuilds whole. It costs a dependency on GitHub's availability for landing (the lander stops with the queue untouched when it cannot dispatch), and a bisection of a red train gates its prefixes in CI one after the other.

**Alternatives rejected.**
- Keep the build host and add a second one: two gates at once ran the build host's VM out of memory, and a second machine is still a fixed number of slots.
- Gate in CI on the `gate` job only (the fallback D1610 had): it skips Windows and macOS, and a train landed with a red Windows shard the day it was tried.
- A merge queue: still unavailable on a user-owned repository (D1592).
- Stop committing the Atlas bundle: `go install` and the release build embed it, and changing that is a release-pipeline change; rebuilding it at pick time removes the conflicts without it.

**Consequences.**
- `HOSTED_JOBS` and `main-status`'s `needs` must name the same legs: `TestSignoffHostedJobsCoverMainStatus` fails otherwise. A leg added to `main` is added to both.
- A leg that is red on `main` itself makes every hosted gate red until it is fixed, and the bisection would then reject an innocent change: fix `main` first.
- The lander's machine needs Node to rebuild the bundle; without it a bundle conflict is rejected as before.
- This amends D1610 (where the gate runs) and D1750 (the implementer's `make check E2E=1`: the train gate runs the scenarios). The signoff's targeted retry and probe (D1755, D1878) apply only to a gate run on a build host.
