---
topic: sync-provisioning
---

# D364 — Antigravity and Hermes get project-local cells; co-owned paths survive a prune

**Decision.** Antigravity projects all five kinds into a workspace: `skill` to `.agents/skills/<n>/`, `agent` to
`.agents/agents/<n>.md` (the global translation), `hook` to `.agents/hooks/<n>/` registered in `.agents/hooks.json`,
`mcp` to `.agents/mcp_config.json` (the global `mcpServers` shape) and `instructions` to the `AGENTS.md` marker block.
The hooks file is chosen from the hook's own path (`antigravityHooksPath`), as Crush's is, and an emptied project
`hooks.json` is deleted; `.agents/hooks.json` is listed as owned beside the cells. Hermes projects `skill` only, to
`.hermes/skills/<n>/`, written like any client's skill (no inbox, no `SOURCE.md`), and the projection is reported
`inactive` unless the workspace is in `skills.trusted_project_dirs` of `$HERMES_HOME/config.yaml`
(`HermesProjectTrusted`, read-only; missing or unparsable means not trusted), with the hint `hermes skills trust <dir>`.
Every provider can now be bound to a workspace. A prune, in `Apply` and when an unbind removes an orphan projection,
skips any path another provider's lock for the same workspace still records (`LockFile.CoOwnedPaths`,
`ApplyOptions.CoOwnedPaths`). This amends D193 decision 11, which left both providers without a project cell.

**Why.** Probed on `agy` 1.3.2 against a negative control (#478): skills, agents, `hooks.json`, `mcp_config.json` and
`AGENTS.md` in the workspace take effect, the same files in a directory without `.agents/` do not. On Hermes v0.21.5
project skills load only after `hermes skills trust <dir>`. `.agents/skills/` is already Codex's project skill cell and
`AGENTS.md` is shared by Codex and OpenCode, so a second provider writing them makes one provider's prune delete a file
the other still depends on.

**Alternatives rejected.** `.agents/rules/` for instructions: read only with `trigger: always_on` frontmatter, a plain
rule file would be written and ignored. Hermes in `.agents/skills/`: co-owned with Codex and Antigravity. A Hermes hook
cell: hooks live in `config.yaml`, operator-owned (D141), and gateway hooks did not fire from the CLI. Copying the
Antigravity agent `tools` / `commandExecutionPolicy` keys as D291 restrictions: the probed agent ran a shell command
anyway. A prune guard only for `AGENTS.md`: the per-name skill files have the same problem. Reference counting per
provider in the lock: the paths a lock records are already the reference.

**Consequences.** Antigravity and Hermes become bindable (`workspace bind`); `ProjectScopeUnsupportedReason` has no
per-provider case left. The Antigravity app was not probed: only the CLI's cells are claimed. A tracked
`.agents/hooks.json` or `.agents/mcp_config.json` makes the hygiene check refuse, as `.mcp.json` does. Only a path
recorded by another provider's lock is protected: a file two providers wrote and one lock lost is still pruned. A
future Hermes `external_dirs` or Antigravity app scope is added by a probe, not by inference.
