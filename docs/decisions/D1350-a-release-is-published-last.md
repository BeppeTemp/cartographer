---
topic: deployment-release
---

# D1350 — A release is published last, after its binaries, cask and image exist

**Decision.** release-please creates the GitHub release as a draft
(`"draft": true`, with `"force-tag-creation": true` so the tag is still pushed and
starts `.github/workflows/release.yml`). A final `publish` job of `release.yml`,
`needs: goreleaser, prerelease-flag, docker`, runs `gh release edit --draft=false`
and reads `isDraft` and `isPrerelease` back; a 0.x release must still be a
Pre-release (D82, D211). `mcp-registry`, which announces the version, now waits
for `publish`. `demo`, `hero` and `candidate-cleanup` keep their needs: none
announces the release.

**Why.** The update check (`internal/updatecheck`, D254) takes the newest
published, non-draft, stable-tagged release. With release-please publishing at
merge, clients and servers were told of a version for the whole pipeline (or
forever, if a job failed) while the cask still named the previous version, the
release had no assets and the image tag did not exist. `updatecheck` already skips
drafts (`Draft` in the releases listing), so the root fix needs no client change.

**Rejected for now.** A channel-aware check (report `latest` for `brew` only once
the tap names it, for `install.sh` only once the platform asset exists): it would
cover tap propagation lag, but is optional on top of this and not needed to close
the window.

**Consequences.** GoReleaser runs with `release.draft: true` and
`use_existing_draft: true` (since v2.5, alongside `mode: keep-existing`): without
the first it edits the release with its default `draft: false` and publishes it
at the upload, without the second it cannot find a draft (GitHub's get-release-by-tag
endpoint does not return drafts) and creates a second release. Every job that
touches the release (the draft check after GoReleaser, `prerelease-flag`,
`publish`) looks it up in the releases listing by `tag_name` and edits it by id,
for the same reason. `hero.yml` and `demo.yml` read no release asset and link no
release (they build from the tag's source and the image), so they stay parallel.
Until `publish` runs, `/releases/latest` and an unauthenticated `/releases` do not
list the version, which is what `install.sh` and `install.ps1` want. A failed job
leaves a draft: re-run it, `publish` follows.

**First run to watch.** After the first release PR merged with this in place:
the tag exists and `release.yml` started; release-please moved the PR's label from
`autorelease: pending` to `autorelease: tagged` and opened no duplicate release PR
(it finds its last release through the tag); the release stays a draft with one
set of assets until `publish` turns it into a Pre-release. Not verifiable before
that run.
