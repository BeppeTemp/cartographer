---
topic: project-governance
---

# D82 — Beta marking via GitHub pre-release flag, not `-beta` version suffix

**Status: implemented (2026-07-20).**

**Context.** Until 1.0 the beta phase should be visible on the releases themselves, not only in the README disclaimer. Candidate mechanisms: a `-beta` semver suffix in the tags, or the GitHub pre-release flag on the releases.

**Decision.** `"prerelease": true` in `release-please-config.json`: every GitHub release is marked **Pre-release** until 1.0, tags stay plain `v0.x.y`. The already-published `v0.1.x` releases were retro-marked for consistency (operator action). The README release badge uses `?include_prereleases` and links to `/releases` (with every release a pre-release, `releases/latest` would freeze on the last stable one). At 1.0.0 the flag is removed and the badge reverted.

**Rationale.** A `-beta` suffix in the tags would re-break `go install @latest` (Go considers prereleases only when no release version exists, and stable `v0.1.x` tags already do — the exact invisibility D80 fixed) and is redundant with 0.x semantics, which already declare instability. The pre-release flag gives the visible marker at zero tooling cost: Go reads tags, not GitHub release metadata.
