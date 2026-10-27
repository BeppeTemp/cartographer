---
topic: sync-provisioning
---

# D1172 — OpenCode's global MCP entry lives in its config directory

**Decision.** The global OpenCode MCP entry, Cartographer's own (`connect`) and a KB's `mcp`
artifact, is written under the `mcp` key of `~/.config/opencode/opencode.json`. An entry an
earlier version wrote to `$HOME/opencode.json` is removed from there: a KB artifact through the
generic relocation (D360), Cartographer's own entry by `connect`/`sync` once the new one is
written, and by `disconnect` from either file. The project cell stays `opencode.json` at the
repository root.

**Why.** Both majors load global config only from their XDG config directory (1.x `Global.Path.config`,
2.x `ConfigDiscovery.discover`, checked on the source of v1.19.4 and v2.1.4; the config docs name
`~/.config/opencode/opencode.json`). `$HOME/opencode.json` is reached only as a project file by
walking up from the session folder: 1.x stops at the git root, so a session in a repository never
saw the entry; 2.x walks to `/`, so a session outside `$HOME` never saw it, and the entry ranked
as project config. The cost is that Cartographer now merges into the user's own global OpenCode
config: a `.json` file there that holds comments is refused (not valid JSON), as for every JSON client.

**Alternatives rejected.** Keeping `$HOME/opencode.json` and documenting it: the entry is missing from
most sessions, which is the bug. Writing both files: wherever the walk-up still finds the old file,
its copy overrides the global one as project config. Leaving the old entry for the user to delete: it shadows the global
one in sessions that still find it, with the URL of the day it was written. Honouring
`XDG_CONFIG_HOME`: no other OpenCode cell does (D360), and changing all of them is its own decision.

**Consequences.** The next `connect` or `sync` moves the entry and says so in a warning; only the
names Cartographer manages leave the old file, which is deleted once nothing but the `$schema` hint
is left. `mcpProviderFromPath` keeps mapping `opencode.json` to OpenCode so an old lock entry is pruned.
