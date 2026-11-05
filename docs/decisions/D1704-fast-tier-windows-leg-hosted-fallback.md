---
topic: project-governance
---

# D1704 — A fast tier for iterating, a Windows leg in the signoff, CI when the build host is away

**Decision.** `make check` is the tier an implementer runs while iterating: fmt, vet and the tests of the packages the checkout affects, chosen by `scripts/affected-pkgs.sh` (the changed packages and every package whose test binary depends on one). `make signoff` gains two variables: `SIGNOFF_WINDOWS`, a command that runs the affected packages' tests natively on Windows beside the targets (a red leg fails the signoff), and the exit code 75 of `SIGNOFF_MAKE`, which means its machine is unavailable: the signoff then pushes, dispatches `ci.yml` on the branch and posts the status only when that run's `gate` job is green.

**Why.** Measured over 85 maintainer PRs (#1600–#1703): all 27 red `main` runs of `ci.yml` came from `test-windows` (23) or a cancelled `test-macos` (4), with four real Windows regressions (in `internal/provisioning`, `internal/agents` and `internal/audit`) that kept `main` red from 40 minutes to 12.5 hours each, because D1610 left Windows to `main`. Those packages test in under two minutes on Windows. Six PRs waited 1.5–2 hours when the build host was off and the CI fallback was done by hand. Implementers ran the whole suite to iterate, on one host that runs a single heavy gate at a time. It costs a Windows machine beside the build host to get the leg (without it the status says `windows not run` and `main` stays the net), and a fast tier that can miss a reader of repository files, which `make gate` still catches.

**Alternatives rejected.**
- The `windows` filter of `scripts/ci-filters.sh` as the Windows package list: `internal/audit` was not in it and broke on Windows.
- `test-windows` back on every maintainer PR (D1592): one hosted runner at a time is the reason D1610 exists.
- Running the affected tests on the build host's checkout: it is a copy without history, so the diff is computed where the branch lives and the list travels as a make variable.
- A fallback that waits for the whole dispatched run: it includes the macOS queue; the `gate` job proves what the local targets prove.

**Consequences.**
- `affected-pkgs.sh` is an accelerator, never a proof: anything that must hold is in `make gate`. A test that reads a repository file outside its package adds its package to the table, pinned by `internal/repodocs/affected_pkgs_test.go`.
- A wrapper behind `SIGNOFF_MAKE`, `CHECK_MAKE` or `SIGNOFF_WINDOWS` exits 75 only when its machine cannot run the command, never for a failing command: a 75 turns a local proof into a hosted one, or skips the Windows leg.
- The status description of a signoff now names the Windows leg; amends D1610 on what a signoff proves.
