---
topic: sync-provisioning
---

# D360 — OpenCode global skills and agents live in its config directory, and a moved destination relocates on sync

**Decision.** The global `skill` and `agent` cells for OpenCode are `~/.config/opencode/skills/<name>/`
and `~/.config/opencode/agents/<name>.md`, the `config` directory `opencode debug paths` reports and the
documented 1.x global paths, so one cell serves both majors. This retires the OpenCode half of D192
(`.opencode/agent`). Project cells and hook files are unchanged. `Apply` relocates any artifact whose
lockfile paths are not under the provider's current destination: it is rewritten at the new one and the old
files are pruned (reported as `moved`). The compat test asserts the declared cells against
`opencode debug paths` and fails, not skips, when the command errors.

**Why.** Probed on OpenCode 2.0.25: neither `~/.opencode/skills` nor `~/.opencode/agent` is loaded; the same
files under `~/.config/opencode/` are. The D192 alarm skipped on 2.x because `opencode agent list` no longer
exists, and treated the error as "not discoverable", so the break went unnoticed.

**Alternatives rejected.** A per-major cell: the config directory works on both, so a branch would only add
code. Moving the project cells too: not probed on 2.x, and changing them by inference is the mistake D192
describes. A one-off OpenCode migration: the next destination move would need the same code again, so the
relocation is generic. Relocating `hook` entries: their lock entries include generated companions outside the
destination directory (the plugin), which would read as "moved" on every sync.

**Consequences.** The next `sync` moves OpenCode skills and agents; the old directories are pruned from the
lockfile paths only, and `~/.opencode` survives while it holds `hooks/`. Any later destination change needs
only the cell edit. The project cells remain a watch item in `docs/harnesses.md`.
