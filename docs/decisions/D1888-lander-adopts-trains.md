---
topic: project-governance
---

# D1888 — The lander reruns a red check once, adopts an open train PR of its own repository, and dequeues a change by its Lander-Slug and Lander-Tip

**Decision.** A required check of the train PR that fails is rerun once (the failed jobs of every completed failed run of the head commit); the lander stops only if a required check is red again after that rerun, and the rerun is spent only once a run was actually rerun. An open `train/*` PR that the lander did not open is adopted when it is a branch of the repository (not a fork's), its base is `main`, its head is the one the PR shows and it is built on the current `main`: it is merged with `--match-head-commit` by the same rule as the lander's own train, then the queue is composed again. One built on an older `main` is closed, as the lander's own trains are; any other (a fork's, a base other than `main`, a head that moved) is only waited on for `LAND_OPEN_TRIES` polls, then the lander stops and says so. A queued change counts as landed when a commit on `main` carries its `Lander-Slug` and the `Lander-Tip` of the queued commit (both exactly), whatever its patch: it is dequeued, not landed again. A change queued without a `Lander-Tip` is matched by its patch only.

**Why.** A check that reads the files or diff of a just-opened PR fails with an HTTP 5xx and passes a minute later (#1881); stopping the lander on it left an open train PR that no lander would merge, and a hand merge left the queue entries to be landed again. The patch comparison cannot see such a landing, because the lander numbers a pending decision anew on each landing (`D-new-<slug>` becomes a different `D<n>`), so the two diffs differ in that file. The slug alone would also match the next version of the same slug, hence the tip.

**Alternatives rejected.**
- Retrying only in the workflows: the lander would still stop on any other transient failure; both are done, the workflows being the cheap half.
- Adopting any `train/*` PR: a fork can name a branch that way, and merging it would land a contributor's code with the maintainer's token.
- Rerunning on every red check: a check that is red for a test would loop; once separates a flake from a failure.
- Matching the landed change by the title: titles repeat across changes.

**Consequences.** `Lander-Slug` and `Lander-Tip` on a landed commit are part of the lander's contract with itself (`scripts/land.sh` writes them, `make worktree-clean` reads them too): dropping either breaks the dequeue. Two landers on different machines can both adopt the same train PR; the second merge is refused by `--match-head-commit` or finds the PR merged, which is harmless.
