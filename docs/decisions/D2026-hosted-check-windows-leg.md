---
topic: deployment-release
---

# D2026 — the hosted check has a Windows leg, always on when Go code changes

**Decision.** `hosted-check.yml` runs a second job, `windows` (`windows-latest`, the setup of `ci.yml`'s Windows legs), in parallel with the Linux `check` job: `scripts/test-shard.sh 4 all <packages>` over the packages of the train gate's Windows leg (`affected-pkgs.sh --code`). `scripts/hosted-check.sh` computes that list itself, from the same changed paths `make check` selects its packages from, and dispatches it as the `win_pkgs` input; the Makefile and the arguments `make check` hands to `CHECK_MAKE` are unchanged. The leg runs whenever the change touches Go code (no opt-in) and is skipped when the list is empty. The verdict is per leg: the script reads both jobs' states from the poll that gives the run's verdict, and a red run names the red leg(s) (`red: Linux`, `red: Windows`) with each one's log excerpt. A list that cannot be computed is exit 1 with the reason, never 75. Amends D1921, which says the script "returns the job's verdict" (now two jobs, one verdict per leg) and rejects reusing `ready-check.yml` because it runs a Windows leg (the hosted check now has its own).

**Why.** #1935: `make check` tested the affected packages on Linux only, so a test green on Linux and red on Windows (a configured directory `/srv/p` is not absolute there) was first seen by the train gate, which rejected a whole change. The hosted runner is the one `CHECK_MAKE` that can run Windows at no cost to the build host, and a leg that runs in parallel adds no wall-clock time beyond the slower of the two.

**Alternatives rejected.**
- A `CHECK_WINDOWS` knob in the Makefile, or the Makefile computing the list: the maintainer decided `make check` stays as it is. Every other `CHECK_MAKE` keeps its arguments, and the script already holds the workflow's contract (it dispatches and validates the inputs).
- An opt-in leg: the point is to catch what only Windows sees before the change is queued; an opt-in is skipped by exactly the changes that need it.
- Running it after the Linux job (`needs:`): doubles the wait for no gain; a red Linux leg does not make the Windows result useless.
- Reusing `ready-check.yml`: it is push-triggered on `ready/**` and derives its packages from the commit, which D1921 rejected for the Linux leg and still does.

**Consequences.** One more (Windows) runner per hosted check; the REST cost is unchanged, because the jobs' states come from the same `gh run view --json status,conclusion,jobs` call as the verdict. The list is computed in `hosted-check.sh`, not in `make check`, so it needs the checkout's `upstream/main` (or `origin/main`) like `make check` does; `HOSTED_CHECK_TEST_WIN_PKGS` in the environment replaces the computation and exists for the tests only (a name no shell exports by accident; the script notes on stderr when it is used, and every run notes the Windows leg's package count or that there is none). The `windows` job validates the list again and takes it from `env`, never interpolated; its actions are pinned like the Linux job's. `make check` itself stays Linux only, and the train gate's Windows leg stays the proof: the hosted leg is an accelerator, like the rest of the fast tier. See D1921.
