---
topic: architecture
---

# D1936 — a digest pinned in the component's config is its consent to run a plugin

**Decision.** The server config and the Surveyor config take `plugins_dir` (the read-only store) and `plugins.<name>`
(`digest`, `enabled`, `settings`). At start each component calls `core.LoadPinned` for its own side; the pin's digest
is the only trust anchor. Any enabled pin that does not load (digest mismatch, missing or modified tree, no such side,
side unsupported here) stops the start with the plugin named, never "loaded with a warning". The shape of the keys is
validated at load (`core.CheckPin`: a name and a 64-hex digest, no unknown key, scalar settings, `plugins` needs
`plugins_dir`), so a malformed pin stops the start before any disk access. The server requires an absolute
`plugins_dir`; the Surveyor also accepts one relative to its config directory. The server reads the pins once at
start, not on the auth reload; both need a restart to change.

**Why.** A headless process has no client install state and nobody to ask: the operator's reviewed digest in the
file that already configures the component is the same kind of consent the client records, and `State` stays the
single rule. Failing closed means a typo or a swapped tree cannot silently drop or replace code that was approved.

**Alternatives rejected.** Loading with a warning on a mismatch: a plugin nobody read would run. A separate trust
file: a second place to keep in step with the config. Reading the pins on every auth reload: a plugin would change
under a running server without the restart the operator expects. The Surveyor loading plugins inside `LoadConfig`:
it has no version of the binary, which the manifest floor needs; `Config.LoadPlugins` takes the options at start.

**Consequences.** The server reads the loaded plugins' roles and presets (`D1937`); the Surveyor
prints one line per plugin and its families come in a later change. The
`surveyor --recipe` dry run does not load plugins. The Surveyor report shows `plugins.<name>.digest` (not secret).
