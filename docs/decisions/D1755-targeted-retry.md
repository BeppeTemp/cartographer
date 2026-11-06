---
topic: project-governance
---

# D1755 — A red signoff runs the packages of the Go tests it can name once more, before anything is gated again

**Decision.** `make signoff` runs its targets under `make -k`. Sometimes make reports a failure itself (exit 2) and every failure in the red log is a named Go test (`scripts/test-failures.sh`). The signoff then runs the packages of those tests once more, whole and uncached, on the machine where they failed. That is `make test-rerun` (the race ones as the `race` target runs them), or `go test -count=1 <packages>` through `SIGNOFF_WINDOWS`. Green: the signoff goes on, prints `signoff: flaky: <tests>` and says `<n> flaky retried` in its status, and the lander opens a `bug` issue naming them. Red: the signoff prints `signoff: confirmed on a targeted retry:`, and the lander goes straight to bisection without gating the train a second time. Some failures get no targeted retry, and the lander still re-runs those once, as before:

- a failure the script cannot name: a make target other than `test` or `race`, a make error of its own, a package that did not build or set up, a panic (a test timeout is one), or a data race report;
- a line that starts like `go test`'s but is not in its exact shape;
- an exit that is the wrapper's, not make's.

**Why.** A red train paid a whole second gate to learn whether one test was flaky. One train took 136 s to go red, then 633 s to re-run `make gate web-gate`, only to find a budget test that had failed under load. Gating again costs the same when the failure is real, so a train of one change is rejected only after two whole gates. Running the failing packages takes seconds to a minute. With `-k`, every other target already ran in the same signoff, so the re-run of the packages answers the question the whole re-run asked. The packages run whole, not just the named tests. A failure that depends on the package's other tests (shared state, ordering, a port) still reproduces. A failure that depends on load from the rest of the gate does not, and that is a flake. The rule for what may be retried comes from AWS's AI-DLC repository, which applies it to its merge queue (`tests/lib/file-retry.ts`): only failed cases with complete evidence earn a retry, a timeout or a crash never does, and a pass on retry is reported by name, never silently.

**Alternatives rejected.**
- Keep the whole re-run: it doubles the cost of every red train to answer a question the log already narrows to a few packages.
- Re-run only the named tests (`-run '^(A|B)$'`): it drops the package's other tests, which may be what made them fail, so it could call a deterministic failure flaky and land it. It also sends a regular expression through the `SIGNOFF_MAKE` and `SIGNOFF_WINDOWS` wrappers, which a wrapper that re-parses its arguments in a remote shell would break. The retry passes only plain words: the packages joined by commas.
- `go test`'s own flags: none of them retries a failed test, and `-count=2` runs every test twice.
- Vitest's `--retry` and Playwright's `retries`: they retry silently inside the run, so a flaky test never reaches an issue. The web and e2e failures keep the lander's whole re-run, which now at least names them.
- Retrying in the lander instead of the signoff: the signoff is where the targets run and where the machine is. Retrying there also means a bisection prefix, or an interactive `make signoff`, fails only on a real failure.
- Retrying a data race: a race report is a defect even when the second run does not reproduce it.

**Consequences.**
- A red signoff takes longer before it says so, because `-k` runs the remaining targets, but it reports every failure at once.
- `scripts/test-failures.sh` has to keep up with the output formats it reads. Those are GNU make's `*** [file:line: target]` lines and `go test`'s `--- FAIL: <Test> (<n>s)` and `FAIL<TAB><package><TAB><n>s` lines; `internal/repodocs/testfailures_test.go` pins them. A new test target that should be retried has to be added there, next to `test` and `race`.
- The log holds the tests' own output, so a test can print the lines the script and the lander read. The script takes a name or a package only in Go's own shape, and the lander files only `package.Test` names. A test that wants to lie about its result can still do so, as it could before. The gate runs the train's code with the maintainer's privileges.
- Amends D1706: a red train is gated a second time only when the signoff could not retry it.
- Amends D1610: a `signoff` status may carry flaky tests that passed on their retry.
