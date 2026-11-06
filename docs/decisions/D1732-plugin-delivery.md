---
topic: sync-provisioning
---

# D1732 — Plugin skills, agents and hooks join each global projection after the KB selection, and an unreadable plugin retires nothing

**Decision.** The skills, agents and hooks of the active plugins join the global projection of every connected provider after the KB selection and `kb_options` have run, and before `DropPluginCollisions` and the strict merge; no workspace projection receives them. A plugin that is enabled and approved but whose approved tree cannot be used in a run (not in the store, modified, invalid, or no store) contributes nothing and its lock entries are carried forward unchanged, so nothing it installed is pruned. A disabled, pending or removed plugin is not unreadable: it leaves the active set and its files are pruned by absence like any artifact. `sync` fetches an approved plugin missing from the store from its configured source and ref and stores the bytes only when their digest equals the approved one; it never approves.

**Why.** A plugin belongs to the machine, not to a KB, so a KB binding or `artifacts: false` must not move it, and a workspace is a repository that must not receive what its owner never asked for. Prune by absence is right for a plugin the user turned off and wrong for a cache that was purged or a repository that is unreachable for a day: the held lock entries give the unreachable-KB rule to plugins. Re-fetching without approving keeps the approved digest as the only authority, so a source that changed cannot install anything between two `plugin` commands.

**Alternatives rejected.** Treating a plugin like a bound KB (a binding per plugin, `kb_options` applying): more configuration for something the user installed once. Pruning an unreadable plugin's files (a purged cache would delete a working setup). Failing the sync on an unreadable plugin (one broken plugin would stop every provider). Fetching the source's current head on `sync` (it would run bytes nobody approved).

**Consequences.** `candidateSet.Plugins` is filled once per pull from `loadActivePlugins`; `ApplyOptions.ActivePlugins` is derived from the plugin artifacts in the manifest of the projection, which only that loader can put there. A new consumer of the manifest passes the same set or `ArtifactAuthorized` refuses a plugin artifact.
