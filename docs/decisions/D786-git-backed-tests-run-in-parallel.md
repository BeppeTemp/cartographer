---
topic: project-governance
---

# D786 — The git-backed test packages run their tests in parallel

**Decision.** Top-level tests in `internal/mcpserver`, `internal/kb` and
`internal/lint` call `t.Parallel()`, except the ones that touch process-global
state, which stay serial (`docs/conventions.md` §Tests).

**Why.** No test in the repository was parallel, so each of these packages ran
hundreds of git-spawning tests one after the other; `internal/mcpserver` alone
was most of CI's wait, on Windows above all (D785). In parallel the three
packages go from 188s to 67s on a developer machine. The cost is a rule every
new test in these packages has to respect, and a class of mistake (a parallel
test racing on a global) that only `-race` or a flake reveals.

**Alternatives rejected.**
- Building each test KB from a copied template instead of `kb.Init`: `Init`
  writes the directory name into the first commit, so a copy changes what tests
  see, and parallelism had already taken most of the time.
- More CI shards alone (D785): they cut the wall time on CI only, not on the
  machine of whoever runs `make gate`.
- Parallel by default everywhere, `cmd/cartographer` included: its tests lean
  on `t.Setenv`, which Go refuses in a parallel test.

**Consequences.** A test that writes a package-level var or hook, sets the
environment, changes directory, swaps stdout/stderr or calls the `kb.InitOn*`/
`InitWith*` setters must not call `t.Parallel()`. If a slow Windows runner makes
a deadline-bound test flaky, `-parallel` on that package is the lever, not
removing the rule.
