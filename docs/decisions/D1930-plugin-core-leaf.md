---
topic: architecture
---

# D1930 — The plugin tree reader reaches no provisioning

**Decision.** The file-path rule of an artifact (`ValidArtifactFilePath`) lives in `provisioning/contract`, next to `ValidArtifactName` and `HashArtifactFiles`; `provisioning` delegates to it. The files of `internal/plugin` that read, digest and pin a tree (manifest, tree reader, store, point registry) import `contract` and no longer `internal/provisioning`; the client's loaders, previews and `ProviderCells` sit in files of their own. `ReadTree` classifies a top-level folder by family, not by registered point.

**Why.** The server and the Surveyor must read a stored tree without linking the half of `provisioning` that writes into home directories (D1884). The rule was already one function; moving it to the leaf the server links keeps a single rule for the sync-pull decode and the plugin tree. Classifying by family makes the digest and the schema verdict of a tree the same in a binary that links no client loaders.

**Alternatives rejected.** Duplicating the path rule in `plugin`: the two copies would drift. Exporting the helpers of the provider descriptor: a wider API than moving the files.

**Consequences.** No observable change: digests, problems and approvals are unchanged (`TestDigestGolden`, `TestSchema1Compat`). The next change moves these files to a package the firewall allows on the server side.
