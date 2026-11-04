---
topic: client-configurator
---

# D1654 — Cline is a provider: the bridge, whole-owned rules, translated agents, no project scope

**Decision.** Provider `cline` (the VS Code extension and the `cline` CLI) is a registry row. MCP goes
through the stdio bridge into `~/.cline/data/settings/cline_mcp_settings.json` with no `cwd`; instructions are a
file Cartographer owns whole, `~/.cline/rules/cartographer.md`; skills are `~/.cline/skills/<name>/`; agents are
translated to `~/.cline/agents/<name>.yaml`. Every destination is home-relative and global, and every project
cell is unsupported.

**Why.** Cline declares no MCP `roots` and starts the bridge in the extension host's folder, so every session is
unbound and gets the provider-default read-only tools. A bound folder would receive skills and instructions that
explain write tools the session does not have, the exposure D1018 closes: a project cell stays unsupported until
Cline declares roots or reads a per-workspace MCP file. The rules directory is one loader shared by
`~/.cline/rules` and `Documents/Cline/Rules` (no path-conditional frontmatter in 4.1.26), so a plain `.md` in
`~/.cline/rules` is an always-on global rule, and it sits under the `.cline` root of every other cell. A Cline
agent is a `.yaml`/`.yml` file with a non-empty body, which the translator guarantees (the description backs an empty
body); `tools` and `model` are Claude's names and are dropped. The settings file is Cline's own (it keeps a `.bak`
and recreates it), so it is never deleted. Whether Cline expands `${VAR}` in headers is unverified, so headers are
reported as unsupported rather than written as a literal.

**Alternatives rejected.**
- Instructions in `Documents/Cline/Rules`: the Documents folder is relocatable (OneDrive, XDG), so the file could land
  where Cline does not look. A KB-declared variant can still point there.
- A shared global `.claude/skills`: Cline reads it only at project level.
- A rules block inside `AGENTS.md`: that is a project file, and the dedicated file keeps the user's own rules apart.
- Honouring `CLINE_DIR` / `CLINE_MCP_SETTINGS_PATH`: a provider home override would split the base-dir rule (D676, D755).
- Writing `cwd` into the entry: it would pin one folder for a global entry; a user who wants a folder-scoped Cline sets it by hand.
- Emitting `type: "sse"`: no KB descriptor or `ServerSpec` carries that transport.

**Consequences.** `.cline` joins the prune boundaries and also holds Cline's own `data/`: only the levels
created underneath are pruned, and a test keeps a neighbouring user rule and the settings file. Detection gains
`ConfigDirGlobs`, a home-relative pattern probe for an editor extension's versioned directory; a leftover directory
of an uninstalled version is a known false positive. The hook cell is unsupported in this change.
