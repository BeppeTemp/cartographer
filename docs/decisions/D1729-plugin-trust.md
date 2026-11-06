---
topic: sync-provisioning
---

# D1729 — A `plugin:` artifact is authorized by its plugin being active, and nothing else

**Decision.** An artifact whose source is `plugin:<name>` is built on the client from the approved tree of an installed plugin. `provisioning` installs it only while `<name>` is in `ApplyOptions.ActivePlugins` (filled from `plugin.State`); there is no per-artifact approval, a signature on one is an error, and `sync_pull` rejects any served artifact whose source is neither the bundle nor the pulled KB. A kind and name claimed by a plugin and by a KB or another plugin is dropped for every claimant and reported; against a bundled artifact only the plugin loses, so a plugin can never make the sync prune a bundled skill; KB against KB stays an error. Every file that compares a source with `"kb:"` or `"bundle"` is classified in a test with a pinned count of comparisons, so a new one fails until someone looks at it.

**Why.** The plugin's approval is of the whole tree, once, by the user; a second per-hook approval would ask the same question again, and a signature would bind the plugin to a key nobody holds. Keying on the active set fails closed: a plugin that is disabled, modified or unreadable contributes nothing, because the default for an unknown source is still "install nothing". Dropping every claimant of a plugin-against-KB collision avoids a silent winner that changes when a plugin is added or removed. It costs one more rule to remember at each source comparison, which the audit test makes mechanical.

**Alternatives rejected.** Per-artifact approval of plugin hooks (duplicates the plugin approval and trains the user to accept). Signing plugin artifacts (no key to pin, and a server could then relabel its own artifacts). Letting the plugin win a collision (the KB's content would change on a plugin install). Letting the KB win (a plugin could never be known to apply). Treating `plugin:` as a KB source with a name (it would be looked up in `KBRoots`, receive `SOURCE.env` and join the D1141 migration).

**Consequences.** Callers build `ActivePlugins` from `plugin.State` and run `DropPluginCollisions` before `MergeArtifactsStrict`; a plugin hook is registered by the same registrars as a KB hook. Plugin agents and hooks are not announced in the instructions block. Any new place that tests a source moves the pinned count in `source_audit_test.go`, which is what makes it classify itself; the test cannot tell a comparison that is correct for a plugin from one that is not.
