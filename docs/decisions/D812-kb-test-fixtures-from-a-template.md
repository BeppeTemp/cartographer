---
topic: project-governance
---

# D812 — A test KB is a copy of a template, built once per test binary

**Decision.** A test that needs a KB and does not assert on what `kb.Init`
does gets one from `kbtest.New(t)` (`internal/kbtest`); `internal/kb`'s own
tests use `newTestKB`. Both write an in-memory snapshot of one `kb.Init`
result, taken once per test binary (`internal/testtree`), into `t.TempDir()`
and `kb.Open` it. The fixture directory is always named `kb`. Production code
never imports `internal/kbtest` or `internal/testtree` (a test in
`internal/repodocs` enforces it).

**Why.** `kb.Init` spawns eleven git processes, and the fixtures built on it
were most of the roughly 27,000 git processes of `go test ./...`: with sys time
above user time, the suite made a laptop run hot and `make gate` take minutes.
A copied template spawns none. The cost is two test-support packages, and a rule
the author of a test has to apply: a test about `Init` itself (layout, identity,
branch, `settings.yaml`, the KB name, an existing or unborn repository) keeps
`kb.Init`, because a fixture would hide what it verifies.

**Alternatives rejected.**
- A template directory on disk, copied per test: it outlives the run or needs a
  `TestMain` in every package to clean up; the in-memory snapshot needs neither.
- One package for both: `internal/kb`'s tests (package `kb`) cannot import a
  package that imports `kb`, hence the leaf `testtree` under `kbtest`.
- Replacing `gitx.IsRepo` with a `stat` of `.git`: it would change behaviour for
  a KB nested in another repository; only the redundant calls on a repository
  `InitOnBranch` has just created are removed.
- A template under D786's objection (`Init` writes the directory name into the
  first commit): the fixed name `kb` settles it, since fixtures already had
  random basenames and no test could rely on one.
- `git init --initial-branch`: it needs git 2.28, which the code does not depend on.

**Consequences.** A new test picks the fixture by the rule above, and a test
that relies on the KB name uses `kb.Init`. The copied `.git/index` carries the
template's stat data, which git refreshes on the first status. The git processes
a tool call itself spawns are out of scope here. How to count git processes is
in `docs/testing.md`.
