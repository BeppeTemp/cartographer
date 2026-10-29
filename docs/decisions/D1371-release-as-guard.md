---
topic: project-governance
---

# D1371 — The milestone names the version: a Release-As guard aligns the release PR

**Decision.** The next train's milestone (`v<x.y.z>`) is the source of the release version. A sibling workflow, `release-as.yml`, runs after `release-please` and, when the release PR proposes a lower version, opens one PR with an empty commit and arms squash auto-merge whose body is `Release-As: <version>`. A higher version is only reported; a version is never lowered. The comparison lives in `scripts/release-as.sh`, the workflow only calls the API.

**Why.** release-please computes the version from commits (`bump-minor-pre-major`): after only `fix:` commits it proposes a patch for a train the milestone names as a minor. Fixing it by hand (#1370) is a weekly chore that is easy to forget. The squash body defaults to the PR body in this repository, so it is set explicitly at merge. The token is the `RELEASE_PLEASE_TOKEN` PAT for the push, the PR creation and the auto-merge: a PR or merge by `GITHUB_TOKEN` starts no workflow, so the required checks would never report and release-please would never rewrite the release PR.

**Alternatives rejected.** A job inside `release-please.yml`: no `workflow_dispatch`, it cannot run after the whole workflow, and its permissions would widen. A `milestone` trigger: it races the title update after a Release-As merge and opens a duplicate. `GITHUB_TOKEN`: see above. Lowering a higher version: a breaking change must not be silently undone.

**Consequences.** `internal/repodocs/release_as_test.go` pins the triggers, the PAT on every writing step and the `Release-As:` body. A Release-As merge during the freeze moves `main`, so a release candidate built on Wednesday is no longer its head and the train skips until the candidate is rebuilt and `rc-ok` re-added; in practice the guard fires on the first push after a release.
