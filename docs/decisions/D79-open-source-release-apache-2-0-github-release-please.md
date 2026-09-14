---
topic: project-governance
---

# D79 — Open source release: Apache-2.0, GitHub, release-please, GoReleaser, ghcr, Homebrew tap

**Status: implemented (2026-07-18).**

**Context.** The project lived on a private Gitea with an internal deploy-only CI (private registry + `homelab-manifests` bump). Publishing it required choices on license, hosting, history, pipeline, distribution and language.

**Decision.**

a) **License: Apache-2.0** (replacing MIT pre-publication, single author — a plain commit). No NOTICE, no per-file headers, **no CLA** (inbound = outbound) and no dual licensing: anyone, including the author's employer, may use, modify, close and monetize freely.

b) **Hosting: GitHub** (`github.com/BeppeTemp/cartographer`) as the **only** source of truth. The public history starts from a single squashed commit (`feat: initial public release`); the internal history remains readable on the archived (read-only) Gitea repo. The pre-existing version (`2.1.2`) lives only in the release-please manifest, not as tags.

c) **Pipeline: GitHub Actions** — `ci.yml` (required `test` check + `pr-title` conventional-commit lint), `release-please.yml` (release PR with semver bump computed from conventional commits; merge ⇒ tag + GitHub Release; needs the `RELEASE_PLEASE_TOKEN` fine-grained PAT so `pull_request` workflows trigger on the bot's PR), `release.yml` (GoReleaser: 4 binaries + `sha256sums.txt`, Homebrew **cask** pushed to `BeppeTemp/homebrew-tap` via `HOMEBREW_TAP_TOKEN`, multi-arch image on `ghcr.io/beppetemp/cartographer`). Both PATs are fine-grained and scoped to a single repo each.

d) **Main protection**: ruleset with mandatory PRs (0 approvals — CI is the review), required `test` check, no force-push/delete, **no admin bypass**; squash-merge only (the PR title becomes the commit on `main` that release-please reads). Daily consequence: no direct commits to `main` in this repo.

e) **Language: English everywhere** (docs, Go comments, e2e fixtures, project CLAUDE.md, skills); future plans and D entries are written in English. User-facing pages were reoriented to external users in the same pass (new `docs/getting-started.md`, two reading paths in `docs/index.md`).

f) **Homelab downstream**: the cluster consumes the public `ghcr.io` image (manifest bump in `homelab-manifests`, Flux reconciliation); the operator release procedure (`deploy` skill) moved out of the repo into the maintainer's local, unversioned tooling. First public release: `v2.2.0`.

**Rationale.** One pipeline instead of two (public release + private deploy were redundant); release-please removes per-release semver judgment; the squashed history avoids exposing internal timestamps while keeping the archive consultable; Homebrew via personal tap because homebrew-core is precluded to new projects (notability thresholds).
