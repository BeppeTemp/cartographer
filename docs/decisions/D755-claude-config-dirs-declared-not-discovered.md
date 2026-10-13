---
topic: client-configurator
---

# D755 — Claude config dirs are declared, not discovered

**Decision.** `clients.claude.config_dirs` in `.cartographer.yaml` lists extra Claude Code config directories (each holding its own `.claude.json`, as under `CLAUDE_CONFIG_DIR`); sync and connect write and remove the `cartographer` MCP entries in `$HOME` plus every declared directory. Only those entries fan out: KB-provided `mcp` artifacts, skills, agents, hooks, `CLAUDE.md` and the bootstrap hook stay `$HOME`-relative.

**Why.** After a KB rename every account kept the stale URL, because nothing wrote outside `~/.claude.json`. Sync also runs from session-start hooks (inheriting one account's environment) and from the timer (inheriting none), so a target set read from the environment would change from run to run and orphan entries. An undeclared `CLAUDE_CONFIG_DIR` is only a warning naming the key to add. The cost: a new account needs one line in the file.

**Alternatives rejected.**
- Discover from `CLAUDE_CONFIG_DIR` at sync time: flip-flops the set between hook, timer and shell runs.
- Reuse `BaseDirEnv`: it replaces the root instead of adding roots, and Claude's layout (`$DIR/.claude.json`) is not `$DIR/.claude/...`.
- Fan out every artifact kind: needs multi-root lockfile projections; users symlink those directories instead.

**Consequences.** `ClientBinding` is an explicit binding only when it carries a `kbs` key (`KBsSet`): an entry holding just `config_dirs` leaves the provider on the default KB set, never default-deny. `config_dirs` under any provider but `claude` is a load error. A declared directory that is missing, relative or holds a non-regular `.claude.json` is skipped with a warning, never created.
