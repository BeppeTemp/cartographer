---
topic: project-governance
amends: D785, D2040
---

# D2415 — `ci.yml`'s `test` runs `make gate` in parts, and a push to main skips the legs its train gate ran

**Decision.** `ci.yml`'s `test` job is a matrix of four legs: `checks` runs `make fmt-check vet race` and the HTTP, stdio, e2e and install smokes, and three shards run the Go tests with `scripts/test-shard.sh 3 <n>` (repository readers included). On a push to `main`, a first `same-tree` job (`scripts/ci-same-tree.sh`) looks for a green `workflow_dispatch` run of `ci.yml` among the last 20 whose head commit has the pushed tree; when it finds one, the heavy legs skip and `gate` and `main-status` read the skip as green. The hosted check splits its Windows leg by `scripts/ready-check-plan.sh --count`, the pre-check's rule, instead of always six shards. `test-macos` is unchanged.

**Why.** The 40 green train gates measured took 15.5 minutes median, and `test` ran fmt, vet, the tests and the race pass one after the other; split, the estimate for the gate's median is about 13.6 minutes, with `test-macos` then the limit. The start delay was 5.2 minutes of a 15.5-minute gate: a train is rebase-merged only onto the `main` it was gated on, so the push run that follows tests an identical tree, and it held about 16 runners for 13 minutes exactly when the next train starts. A hosted check held eight runners whatever the change's size. The cost: two more jobs per gate, and `main` loses one free flaky sample per train.

**Alternatives rejected.**
- Two shards: fewer runners, but each shard carries half of the tests where three carry a third; three matches the `checks` leg's fmt, vet, race and smokes more evenly (an estimate, to be read on the first gates).
- Shorten `test-macos` by moving the Cask test to the nightly: a loss of coverage at the gate, left for its own decision.
- Skip the push run by commit sha: a rebase gives the train's commits new shas on `main`; the tree is what the gate tested.
- Read a skip as red in `main-status`: the run on the same tree already said green.

**Consequences.** `same-tree` fails toward running every leg: any read it cannot make runs them all. A pull request, a merge-queue run, a dispatch and the nightly never skip. A new step of `make gate` must be added to one leg of `test`: `internal/repodocs` (`GateParts`) checks that the legs together run the whole gate. `HOSTED_JOBS` still names `gate`, which collects the matrix's result.
