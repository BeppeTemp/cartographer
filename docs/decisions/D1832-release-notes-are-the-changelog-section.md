---
topic: deployment-release
---

# D1832 — The published release notes are the CHANGELOG.md section of the tag

**Decision.** The `publish` job of `release.yml` sets the release body, in the same
PATCH that un-drafts it, to the `## [<version>]` section of `CHANGELOG.md` at the tag,
extracted by `internal/changelog/gen -file CHANGELOG.md -section <version>`. The
extraction runs in a step with no secret and no credential (D1020); the step holding
`GH_TOKEN` runs `gh` only.

**Why.** `CHANGELOG.md` is grouped by area and filtered on the release branch whatever
the size of the notes (D865). release-please's own notes file is ungrouped, and above
the PR body limit the release PR body is a placeholder with nothing to group, so v0.28.0
went out flat, with entries the grouping drops on purpose (#1819).

**Alternatives rejected.**
- Group `release-notes.md` on release-please's notes branch: depends on release-please
  internals (placeholder text, branch name) and leaves two copies to keep equal.
- Check that the notes match and fail: the same fix by hand, every time.

**Consequences.** A body over GitHub's 125,000 characters fails the job before
un-drafting, so the release stays a draft. The release PR body stays what release-please
writes. A tag whose `CHANGELOG.md` has no section for it cannot be published.
