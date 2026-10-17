---
topic: project-governance
---

# D858 — Implementation PRs are reviewed by `pr-reviewer` before the coordinator reads them

**Decision.** The PRs that `implement-issue` and `live-dev` open go through the
`review-pr` procedure (§3–§5): `triage.sh` gives the tier, standard and full tier
get `pr-reviewer` rounds (at most three), and the coordinator verifies the
findings and reads only the triage, the cited lines, the invariants ranges and
supply-chain hunks. It no longer reads the whole `gh pr diff`; light tier is
unchanged. Fixes are made by the author (the `plan-implementer`, or in `live-dev`
the coordinator or a `live-implementer`), who only ever receives verified
findings, and they are added as commits, never amended. The coordinator writes
the PR's `## Self-review` section after the last round.

**Why.** The full-diff read on the most expensive model is the cost D856 built
`review-pr` to avoid, and the `plan-implementer` cannot spawn a reviewer, so its
Self-review was never independent. `live-dev` used the client's generic
`/code-review`, which does not know the area invariants and differs per client.
The cost is one more round trip per PR and a coordinator that sees less code.

**Alternatives rejected.**
- Keep the coordinator's full read and add the reviewer on top: pays for both reads.
- Let the implementer spawn its own reviewer: not possible on every client, and
  an author's own reviewer is not an independent look.
- Amend in fix rounds: rewrites the commit the next round diffs against.

**Consequences.** A fix round needs the implementer reachable (`SendMessage`, or
a new `plan-implementer` with a fix mandate); the squash merge hides the extra
commits. Supply-chain hunks are still read whole, by the user too (D857).
