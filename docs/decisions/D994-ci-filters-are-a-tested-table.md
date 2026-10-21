---
topic: project-governance
---

# D994 — CI path filters are a tested table, and nothing runs twice

**Decision.** Which slow CI jobs a PR runs is decided by `scripts/ci-filters.sh`,
a table fed with the changed paths and pinned by `internal/repodocs/ci_filters_test.go`,
not by regex inline in `ci.yml`. `web` skips `_test.go` files and the packages
`bin/cartographer` cannot import (`internal/{changelog,repodocs,testkbs,webui/gen}`,
compared with `go list -deps` by the test); `demo-edge` is a job of its own for
the demo Worker and its Terraform; the docs workflow ignores `web/e2e/**`; every Makefile target
is run by a workflow or declared local in a test.

**Why.** Every Go change ran Playwright on two runners although the browser
suite drives only the binary, a Worker-only PR skipped its tests, and A filter nobody tests drifts silently;
a test that compares the exclusion list with the import graph breaks when a
package starts or stops reaching the binary.

**Alternatives rejected.** Keeping the regex inline in `ci.yml`: not testable
without GitHub. Excluding all of `internal/` tests and Go from `windows` too:
`cmd/` and the platform packages carry the platform code, so `windows` is kept
and only pinned. Limiting `privacy` to `push` on `main`: a branch pushed without a PR would go unscanned. Splitting the release-please action in two so the release PR gets one
CI run: blocked, the repository setting "Allow GitHub Actions to create and
approve pull requests" is off (see #994).

**Consequences.** A new `internal/` package that the binary does not import must
be added to `not_in_binary` in the script and to the test, or the test fails with
that instruction. Narrowing never removes a check from `main`, where every job runs.
