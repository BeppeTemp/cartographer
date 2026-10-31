---
topic: deployment-release
---

# D1443 — A maintenance release comes from a release-<x.y> branch with its own release PR

**Decision.** A patch release `x.y.(z+1)` that must not carry what `main` already holds for the next minor is cut from a branch `release-<x.y>`, made from the `vx.y.z` tag with cherry-picks (`-x`). `release-please.yml` runs on `main` and on `release-<x.y>` (filter `release-[0-9]+.[0-9]+`, which release-please's own `release-please--*` branches do not match) with `target-branch` set to the pushed branch, so the branch gets its own release PR `release-please--branches--release-<x.y>`; an empty commit with `Release-As:` names the version. Every workflow that looks up a release PR selects it by its full head name, never by the `release-please--` prefix. The release PR is merged by hand with the security gate of CONTRIBUTING.md §Releases cleared; the tag runs `release.yml` unchanged.

**Why.** `main` held the worker for 0.28 while fixes and per-KB client settings were ready to ship as 0.27.1. Releasing from `main` would ship the worker; a hand-made tag and draft would skip the changelog, the decision index and the draft-then-publish order of D1350. The cost: the branch carries its own copy of `release-please.yml` (a push runs the pushed branch's file), `release-train` does not serve it, and `main`'s next changelog lists the backported entries again because `main` never sees the patch tag.

**Alternatives rejected.**
- Tag and draft release by hand on the branch: no changelog, no grouped body, easy to get D1350's order wrong.
- Revert the worker on `main`, release, re-apply: rewrites the history of a protected branch's work and risks the reapply.
- Extend `release-train.yml` to a maintenance branch: the freeze and the `rc-ok` check are defined against `main`'s head; a rare manual path does not justify parametrising them.

**Consequences.** A maintenance release is cut only while it is the highest version: `release.yml` moves the image's `latest` tag and the Homebrew cask. Its candidate (`preview.yml` on the branch) opens the freeze on `main` until the release deletes it (D1384). A new workflow that finds the release PR must select by `release-please--branches--<branch>`.
