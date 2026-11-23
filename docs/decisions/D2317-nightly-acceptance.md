---
topic: project-governance
---

# D2317 — The scripted acceptance lanes run on main every night, and no workflow calls a model

**Decision.** `nightly.yml` runs, once a day on `main`, the acceptance lanes that need neither a private machine nor a model: the all-in-one battery (`make e2e-nightly` and `test/smoke/http.sh` on a release-shaped binary), the quarantined tests outside the verdict, and the upgrade from the previous stable release. A red lane opens or comments the `nightly-red` issue, the next green night closes it, and a night with no commit since the last green one runs nothing and stays green.

**Why.** A defect those lanes find was found when the release candidate was cut, days after the commit that caused it. Running them on `main` names the range that broke it the next morning. The workflow holds no secret and no deployment environment: the lanes that need an agent (the usage test, the sandbox tester, the docs auditor) stay in the coordinator's own session, so there is no recurring cost and no key to guard.

**Alternatives rejected.** A nightly key for the scripted usage lane or the docs auditor: a credential and a bill for a lane whose findings the coordinator reads anyway. A run on every push: the lanes take longer than the gate and `main` already has `main-status`. One job per lane building its own binary: the battery and the upgrade must judge the same artifact.

**Consequences.** `test/e2e/run.sh` takes `E2E_PREBUILT=1` to run the binary already at `bin/cartographer` instead of rebuilding it, or the release-shaped build would be replaced by a plain one. Every job of the workflow refuses a ref other than `main`, since a dispatch runs the file of its own branch; only `report` holds `issues: write` and it checks out nothing (`TestNightlyRunsOnScheduleReadOnlyAndHoldsNoSecret`). A defect found from a red night carries `Found-by: nightly`.
