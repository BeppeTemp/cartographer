---
topic: project-governance
---

# D328 — Feature PRs skip the Windows leg and the decision index; the release PR carries both

**Decision.** On a feature PR, CI runs `test` (ubuntu, the full `make gate` plus
the smoke, e2e and install suites), `web` when the frontend or server changed,
and `pr-title`. `test-windows` runs on every push to `main` and on the
release-please PR, where it stays a required check. `docs/decisions.md`'s
generated index is no longer regenerated in feature PRs: a workflow regenerates
it on the release PR after release-please updates it, and only there does CI
check that it is current (`CARTOGRAPHER_STRICT_DECISION_INDEX`). Before merging
a batch, the coordinator rebases each PR onto the `main` that holds the previous
merges and runs the full `make gate` locally.

**Why.** Landing plans in parallel showed that the slow job and the failing jobs
were different problems. `test-windows` took about eight minutes and was the
whole wait of every Go PR, for a class of regression (platform-specific code)
that is rare and small. Every failure in the same batch was a conflict between
PRs that each passed alone: the committed index went stale with each sibling
merge, and two PRs together pushed the tool catalogue past its budget (D285).
Re-running CI does not catch either before merging, because GitHub tests each
PR's merge ref against the `main` of that moment. A local gate on the rebased
tree does, in about two minutes.

The cost: a Windows-only regression reaches `main` and is seen right after the
merge instead of before it, and a release cannot ship until it is fixed. The
decision index on `main` lags by the decisions added since the last release;
the files themselves are always there.

**Alternatives rejected.**
- A merge queue: it tests the combined tree, but it is not available for this
  repository, and it would re-run the slow Windows leg for every queued PR.
- Running Windows only when platform-specific files change: the whole suite
  runs on Windows precisely because path, lock and process behaviour leak into
  code that is not in a `_windows.go` file.
- Generating the index outside git (at doc-build time): readers on GitHub lose
  the browsable list.
- Committing the regenerated index to `main` from CI: `main` is protected, and a
  bot with a ruleset bypass is a wider permission than the problem needs.

**Consequences.** Feature PRs and plan issues never run `make decisions-index`.
The release PR is the full pass: if it fails on Windows, fix that before the
release. The `changes` job no longer decides the Windows leg.
