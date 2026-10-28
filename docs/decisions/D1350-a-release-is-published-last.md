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

**Consequences.** GoReleaser (`release.mode: keep-existing`) and `gh` attach to and
edit the draft by tag. Until `publish` runs, `/releases/latest` and an
unauthenticated `/releases` do not list the version, which is what `install.sh`
and `install.ps1` want. A failed job leaves a draft: re-run it, `publish` follows.
