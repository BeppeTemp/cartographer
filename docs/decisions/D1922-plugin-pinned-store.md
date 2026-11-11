---
topic: architecture
---

# D1922 — plugin state does not depend on the client config, and a vendored store is read-only

**Decision.** `plugin.State` takes an `Entry` (`PluginEnabled`, `PluginApproved`) instead of the client's
`clientconfig.PluginEntry`, which implements it; `internal/plugin` imports no component config. `Store` has a
`ReadOnly` mode in which `Put` and `GC` return `ErrReadOnly` and `Open` is unchanged. `plugin.LoadPinned` opens the
enabled pins of a headless component through a read-only store, runs `State` on each and returns the active plugins
that have the component's side plus a problem per pin that does not load. `cartographer plugin vendor <source> --to
<dir>` fills such a store and prints the digest and the config lines; it writes no approval and takes no client lock.

**Why.** The server and the Surveyor have no `.cartographer.yaml` and no install state; the operator's pin is the
consent. Keeping `State` the single rule means a headless component cannot invent a looser one. A read-only store
makes sure a running component can never write into the tree an operator reviewed, and a vendored directory is
filled by one deliberate command, outside any lock the client holds.

**Alternatives rejected.** A second state function for headless components: two rules drift. Copying a client entry
into the component config: carries install fields (source, installed) the component never uses. Letting a headless
component fetch: it would load code nobody read.

**Consequences.** A pin whose plugin is not `active`, has no such side or whose side this build cannot load is a
`PinProblem` and the caller refuses to start (fail closed); a disabled pin is skipped. A malformed digest is refused
before any disk access. The config keys that carry the pins are a later change.
