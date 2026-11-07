---
topic: control-plane
---

# D1757 — A provider with an active plugin tool uses the stdio bridge, with the access it had

**Decision.** Plugin tools are served by the local stdio bridge (D737), so a provider whose
`plugins:` entries include an enabled, approved plugin with a non-empty `tools` hint gets the
bridge as its global MCP entry even with no workspace binding (`Config.UsesBridge`). The hint is
written next to `approved` by every site that approves a tree (`plugin approve`, `update`,
`install --yes`, the interactive install) from the load those commands already run; it selects the
transport and grants nothing, since the bridge loads the real plugin. A provider with no binding
and no access profile, and not in `scope: workspace`, whose session runs in the bridge (`Config.BridgeIsHTTPEquivalent`) gets the
scope its HTTP entry gave, decided from that shape and not from the live plugin state, because the
entry changes only at the next sync (after `plugin disable` or `remove` the bridge entry outlives
the plugin and must not turn read-only meanwhile): the provider's KB set, read-write, `kb_options` read-only KBs kept, no roots question,
no "unbound folder" note, `unbound_access` not applied (`mcpbridge.pluginOnlyScope`). It reports
isolation `none`. A provider with an access profile (D1184) keeps its HTTP entry and sees no
plugin tool, as does a client connected by hand with a direct URL; a provider with a binding is
unchanged.

**Amends D737**: a provider with no binding no longer always keeps its HTTP entry.

**Why.** Without it plugin tools would be invisible to every user who never bound a folder. The
bridge's own unbound scope is read-only by default and asks the client for roots, so switching such
a provider over unchanged would silently make its sessions read-only: the new scope exists so the
entry type changes and nothing else does.

**Alternatives rejected.** Serving plugin tools from the server: the plugin runs on the user's
machine with the user's files, so only a local process can. A separate second MCP entry for plugin
tools: another entry per client to write, drift-check and remove, and two servers for one name.
Reusing the unbound scope with `unbound_access: write`: it would apply a setting that never applied
to that provider and still append the unbound note.

**Consequences.** A `scope: workspace` provider is excluded from that scope because its bindings
may all be derived from a KB's `paths.yaml`, and a lost, foreign or outdated derived file loads as an
empty state: reading it as "no binding" would open the whole KB set read-write in the folder it was
bound to, so it keeps the read-only unbound default. A `scope: provider` provider whose last explicit
workspace is unbound keeps its bridge entry until the next sync, and in that window it serves
read-write (the HTTP entry's access) where before it was read-only with the roots question. `cartographer` must be on `PATH` at sync time, or the provider keeps the HTTP
entry and sync warns (naming plugin tools). Any new place that records `approved` must record
`tools` with it (a test pins the install path). A bridge scope that made unbound sessions
read-only must never be reached for a plugin-only provider. Completed by the change that serves the
tools themselves (collision rules, read-only sessions).
