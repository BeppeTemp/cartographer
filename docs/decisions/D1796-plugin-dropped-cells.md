---
topic: client-configurator
---

# D1796 — the allow_symlinks of the client file are read before Load, for the plugin resolution only

**Decision.** `registerPluginProviders` resolves plugin providers before the full `Load` (D898), so it reads
`allow_symlinks` through `clientconfig.LoadAllowedSymlinks` and sets the safewrite allow set for the duration of
the resolution, then restores the set it found. The light reader gives at most what `Load` gives: `providers.<p>`
replaces `clients.<p>`, and one invalid entry empties the answer. A cell the resolver still leaves out is kept as a
reason (`ResolvedProvider.LeftOut`) and said by `sync`, `connect`, `status` and `doctor`.

**Why.** A link the user listed was refused by the resolver because the allow set was registered after it, and a
cell lost to an unlisted link was dropped without a word. Moving the whole allow registration earlier is impossible:
`Load` drops the provider names the registry does not know, which is why the registration runs first.

**Alternatives rejected.** Re-resolving after `registerAllowedSymlinks`: the registry would briefly hold a provider
without the cell. Keeping the light set in place: a re-registration after a plugin change would widen the trust a
validated `Load` gave. Printing at registration: it also runs for `serve` and the bridge.

**Consequences.** The light reader must stay a subset of `Load` (a test pins it). A new place that trusts a link
before `Load` has to restore what it replaced.
