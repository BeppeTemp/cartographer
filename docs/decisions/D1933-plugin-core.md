---
topic: architecture
---

# D1933 — The server links a plugin core, never the client's plugin package

**Decision.** `internal/plugin/core` holds what the server and the Surveyor need of a plugin: the manifest, the families and the point registry, `ReadTree` and its digest, the pinned store and the provider descriptor with its resolver. `internal/plugin` stays the client package built on it (loaders, previews, acquisition, diff, `ProviderCells`). The firewall test lets the server side import the core and keeps forbidding `internal/plugin`, `internal/provisioning` and `internal/mcpbridge`; it also checks that the core itself reaches none of them, and `surveyor` and `surveyorconfig` join its loop. This extends D1884 without superseding it.

**Why.** Headless approval and server-served plugin data make `config` and `surveyor` read and pin a tree, and `internal/plugin` reached `provisioning`, the half that writes into home directories. The core is wider than the pin because `ReadTree` builds `Contribution`, which carries the provider descriptor, and the point registry is read by every loader a server registers.

**Alternatives rejected.** A narrow `pluginpin` package: it would leave the manifest, the tree reader and the registry on the wrong side, and export a dozen helpers. Forwarding aliases of the moved types in `plugin`: two names for one type make the firewall unreadable and invite drift. Duplicating the artifact file-path rule: it moved to `provisioning/contract` in the previous change so the two checks stay one rule.

**Consequences.** A client-only import creeping into a core file fails `TestServerNeverLinksTheApply` at any depth. Loaders register from `internal/plugin`'s `init`, so the digest and the schema verdict of a tree are the same with or without them (`TestReadTreeIndependentOfLoaders`); the cmd binary must keep importing `internal/plugin`. A method cannot be declared across packages, so what the client adds to a core type is a function (`ProviderCells`). `ReadFolder`, `Ignored` and `GlobProblem` are exported for the client's acquisition and its dialect test.
