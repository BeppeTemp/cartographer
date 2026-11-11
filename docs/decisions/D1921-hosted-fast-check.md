---
topic: deployment-release
---

# D1921 — the fast-tier check can run on a GitHub-hosted runner, opt-in and never a gate

**Decision.** `CHECK_MAKE=./scripts/hosted-check.sh` (`make check-hosted`) runs `check-run` on a GitHub-hosted runner: the script pushes the worktree state as a temporary commit (tracked changes and staged new files, never untracked ones: the branch is public) to `check/<slug>-<worktree hash>-<sha12>`, dispatches `hosted-check.yml` on it, waits, prints the failing log excerpt, returns the job's verdict and deletes the branch on every exit path. Exit 75 means only that GitHub could not run the command (extends D1704), never a red check. The check is not a gate and a `check/*` branch never lands.

**Why.** The build host has one global slot, so the checks of parallel implementers queue behind each other and behind the train gate (141 minutes of waiting over two days, 10 minutes at worst). The repository is public: hosted runners are free and parallel. Costs: a push of every checked state, and a round trip of a minute or two more than a local run.

**Alternatives rejected.**
- Push trigger on `check/**`: the package list and the e2e flag would travel in the commit; dispatch carries them as typed inputs and reuses the pattern of `make signoff`. It also leaves `privacy` as the only workflow a `check/*` push fires (its `push:` has no branch filter), which is wanted: the pushed commits are public and filtering it out would weaken a gate.
- Checking HEAD: `make check` runs on uncommitted work, so HEAD would test something other than what `CHECK_PKGS` was computed for. The temporary commit is built with plumbing in a private index and never touches the user's index, HEAD or reflog.
- Reusing `ready-check.yml`: it is push-triggered on `ready/**`, derives its packages from the commit and runs the Windows and web legs.
- Making hosted the default: the build host stays the default for the train gate and web builds; a test pins `CHECK_MAKE ?= $(MAKE)`.

**Consequences.** The run to follow is the first newer than the newest one the branch already had, so a finished run of the same state is never read as the verdict. The workflow holds no secret and `contents: read`, so the branch is deleted by the script, not the workflow; a test pins the triggers, permissions and the `check/` guard. The dispatch needs the workflow on the default branch, so the first use follows the landing. A change that edits `hosted-check.yml` tests its own edit, which grants nothing new. A server-side ruleset that lists branch prefixes must allow `check/*`.
