---
topic: project-governance
---

# D80 — Versioning reset to 0.x: the public line starts at v0.1.0

**Status: implemented (2026-07-18).**

**Context.** D79.b carried the internal pre-publication version (`2.1.2`) into the release-please manifest, so the first public release came out as `v2.2.0`. Two problems. First, semantics: the project is a beta, and a 2.x number signals a maturity (two stable major cycles behind it) that the public project does not have — the 2.x history belongs to the archived internal line, not to what users can observe. Second, tooling: the module path is `github.com/BeppeTemp/cartographer` **without** the `/v2` suffix that Go modules require for v2+ tags, so `v2.2.0` was invisible to `go install`/`go get` (`@latest` resolved to a pseudo-version of `main`, never to the release).

**Decision.** The v2.x public line is retired and public versioning restarts at **v0.1.0**:

a) Manifest reset to `0.0.0` and `bump-minor-pre-major: true` + `initial-version: 0.1.0` in `release-please-config.json` (with no tag matching the manifest, release-please takes the initial-release path and would otherwise default to `1.0.0`): the next release PR computes `0.1.0`. Pre-1.0 semantics: **breaking changes bump the minor** (0.x convention), fixes bump the patch; `1.0.0` when the MCP tool surface and CLI stabilize.

b) The `v2.2.0` GitHub release and tag are deleted (operator action: they predate this decision and would otherwise remain "latest" and confuse the badge and `releases/latest`). The `ghcr.io` image tag `v2.2.0` and the Homebrew cask `2.2.0` are left as-is — registries are append-only in spirit and the next release overwrites `latest`/the cask. Homebrew will not auto-downgrade; irrelevant at current adoption.

c) `CHANGELOG.md` reset with a pointer to this entry; beta disclaimer added to the README (top, GitHub alert) and the pre-1.0 policy stated in `CONTRIBUTING.md`.

**Rationale.** A 0.x number is the only version string that tells the truth about stability, fixes the Go-tooling invisibility for free (0.x tags need no module-path suffix), and preserves headroom: the eventual real `v1.0.0`/`v2.0.0` stay meaningful. Resetting now, days after publication and before any registry (MCP Registry publish shipped after `v2.2.0` and never ran), is the cheapest moment it will ever be.
