---
topic: deployment-release
---

# D303 — A preview is an image built on demand, not a pushed tag

**Decision.** An unreleased `main` is tried on a real deployment through the
manually dispatched `preview.yml` workflow, which publishes only
`ghcr.io/beppetemp/cartographer:<vX.Y.Z-suffix>`. Nothing else is released.

**Why.** Validating a release against a real KB before the release PR is merged
leaves room for fixes that ship in the same version. A pushed `v*` tag cannot do
that: it runs the whole of `release.yml`, which moves `latest`, publishes the
Homebrew cask every macOS user upgrades to and adds an MCP Registry entry, none of
which can be taken back quietly. The cost is a second workflow that has to keep
the same build arguments as the `docker` job in `release.yml`.

**Alternatives rejected.**
- A pre-release tag (`v0.18.0-rc.1`): it triggers every publishing job above,
  and release-please would then compute the next version from a tag it did not create.
- Building and pushing from the maintainer's machine: it needs a local container
  runtime with amd64 emulation and a token with `write:packages`, and it produces
  an image no workflow log can account for.

**Consequences.** The tag must carry a pre-release suffix, so a preview can never
overwrite the image of a real release; the workflow refuses anything else. A
macOS client for the preview is built from source, since there is no cask for it.
A change to the `docker` job in `release.yml` must be mirrored in `preview.yml`.
