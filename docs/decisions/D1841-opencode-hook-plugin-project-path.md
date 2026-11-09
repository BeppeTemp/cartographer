---
topic: sync-provisioning
---

# D1841 — An OpenCode hook plugin is written where the scope's client reads it, and a moved plugin relocates on sync

**Decision.** The generated hook plugin of OpenCode is `~/.config/opencode/plugins/cartographer-<name>.js` in the
global scope and `<workspace>/.opencode/plugins/cartographer-<name>.js` in the workspace scope, for both majors.
`hookMechanism.pluginPath` and `register` take the scope; the synthetic hooks (bootstrap, write-findings) are
applied to the home directory and keep the global path. `Apply` relocates an OpenCode hook whose lock records its
plugin at another path than the scope writes today: the plugin is rewritten at the new path and the old one is
pruned (`moved hook/<name>`), and a prune removes the path the lock recorded, not a re-derived one. This amends
D59 (the plugin path) and D360 (hooks were excluded from relocation; the exclusion shrinks to the other providers).

**Why.** A workspace's base directory is the repository, so the plugin landed in `<workspace>/.config/opencode/plugins/`,
which OpenCode does not read: a project's plugins are `<project>/.opencode/plugins/*.js` (probed on 2.2.5), and the
hook never fired (#1827). Moving the path alone would have changed nothing for a workspace already synced, because
a hook's entries were never relocated.

**Alternatives rejected.** Probing 1.x before choosing a path for it: the project directory is documented for 1.x,
and D360 argued one cell serves both majors (docs/harnesses.md says "documented, not probed"). Relocating every
hook entry regardless of provider: the other providers' companions sit outside the hook directory by design.

**Consequences.** The OpenCode plugin path depends on the scope everywhere it is derived (`openCodePluginRelPath`,
`HookPluginRelPath`); a new reader of it takes the scope from the lock (`Lock.ScopeOf`). The exclude list of a
workspace covers `.opencode/plugins`. The project path on 1.x stays unprobed until a probe says otherwise.
