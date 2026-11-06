---
topic: project-governance
---

# D1750 — The signoff runs smoke-http, smoke and e2e; only test-install is first run on main

**Decision.** `make signoff` runs `gate smoke-http smoke e2e` (plus `web-gate` when the diff needs it), so the train gate the lander runs is the same set CI runs on a contributor's PR minus `test-install`. `ci.yml` keeps running all of them on push to `main`. This amends D1610 on one point: of the four post-merge targets it named, `test-install` alone runs only on push to `main`.

**Why.** A change that broke an e2e scenario landed green and turned `main` red afterwards: `24_generic_mcp_client` went stale after D1724 and stayed red across several trains (#1722), because the lander's bisection only sees what the signoff runs. The three targets take about 50 seconds together in CI, little against the gate, and a red one is a test failure the lander already knows how to bisect. It costs that time on every train and requires the gate host to carry what the harnesses need (bash, curl, jq, git).

**Alternatives rejected.**
- Keep them post-merge and fix `main` by a follow-up: the red range made every later train's CI blame unclear, which is what #1722 showed.
- Add `test-install` as well: it is the slow suite and covers packaging, which a Go change rarely breaks; `main` still runs it.

**Consequences.**
- A missing tool on the gate host fails `make` like any target: the signoff reports `failed (exit N): nothing pushed, nothing posted`, which `scripts/land.sh` reads as a red test (re-run, then bisect). A host that is unavailable is still exit 75 and runs the gate in CI.
- The status description names the four targets; a test pins the list in `scripts/signoff.sh`.
- A target added to the post-merge list of D1610 is a decision, not an edit of `TARGETS`.
