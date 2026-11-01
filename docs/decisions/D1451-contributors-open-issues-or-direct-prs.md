---
topic: project-governance
---

# D1451 — Contributors open issues or direct PRs, and plans are a maintainer's tool

**Decision.** A contributor opens an issue or a direct PR. No plan and no assignment is needed, whatever the size; the 500-line cap stays. The `plan-gate` check keeps its name, because the ruleset requires it, and now enforces only that cap. Triage marks an issue `accepted`, and the issue is assigned to a maintainer when work starts. Plans remain a handoff between a maintainer's sessions, as an issue or as a file (D1450).

**Why.** Most plans were a maintainer's handoff to an agent, closed within a day, and almost none were implemented by someone else. A public plan step for contributors cost a design round on every change over 150 lines, while the maintainer's own work never needed it. The cost is real: this removes the design review before code that D856 asked of a contributor. A wrong design is now found in review and the PR is closed, and the cap limits how much work that throws away. A contributor also cannot see a maintainer's plan until its PR opens; assignment is the only signal that an accepted issue is taken.

**Alternatives rejected.** Keeping public plans for contributors only: the maintainer's process would move and this one would stay behind, with its gate, for a handful of PRs. Accepting PRs only on accepted issues: it blocks a contributor who has a working fix, which is the contribution worth having. Closing external PRs: a contributor brings real value here.

**Consequences.** Amends D856 and D955: the rule that a contributor's change over 150 lines closes an approved plan is gone, and so are the assignment rule and the job that re-ran the gate on approval (D1058). Roles still follow GitHub permissions, and a contributor's agent still stops at an open PR. A contributor may split a large feature into PRs under the cap with no design recorded anywhere, so a reviewer may ask for the design in an issue first.
