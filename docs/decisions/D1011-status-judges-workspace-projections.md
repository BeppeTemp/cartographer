---
topic: sync-provisioning
---

# D1011 — status judges a workspace-scoped provider projection by projection

**Decision.** For a provider in workspace scope (D193), `status` builds the same
per-projection manifests sync applies (`manifestsForProjections`) and compares the
bundle-only global projection with the global lock and each workspace with its own
lock, merging the findings into the provider's card.

**Why.** It compared one provider-wide manifest, built from every bound KB, with the
global lock, which in workspace scope holds only the transversal bundle: a clean sync
reported drift and exit 1, listing every KB artifact as missing.

**Alternatives rejected.** Filtering the provider-wide manifest to the bundle and
ignoring workspaces: it would hide a workspace that lost a file. Filtering that
manifest by KB per workspace: it re-implements the per-projection strict merge and
fails on a collision legal across workspaces (D193 decision 6).

**Consequences.** `status` and `sync` share one definition of what a projection
receives; a change to `projectionsFor` changes both. A bound workspace that is gone
reports drift (sync would refuse it).
