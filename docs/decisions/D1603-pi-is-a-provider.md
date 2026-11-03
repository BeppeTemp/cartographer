---
topic: client-configurator
---

# D1603 — Pi is a provider

**Decision.** Pi (`pi`, `@earendil-works/pi-coding-agent`) is provider `pi` with global `mcp`,
`skill` and `instructions` cells under `$HOME/.pi/agent`, and a project `instructions` cell
(`AGENTS.md`) only; `agent` is unsupported (Pi has no subagents) and `hook` waits for a generated
extension. The MCP entry goes through the stdio bridge (D737) and carries `"exposure": "direct"`.
`${VAR}` in `env`/`headers` is written verbatim and a whole-value `!command` is refused. A `pi` on
`PATH` is detected only when `pi --version` prints a bare version.

**Why.** Pi's default exposure, `codemode`, connects the server but does not declare its tools to
the model, so an entry without `direct` is useless. `${VAR}` is the form Cartographer writes itself
and exactly the one Pi documents, so encoding it would break authentication; what executes at load
is the `!command` form, which a KB-provided `mcp` artifact must not be able to carry. Other tools
ship a `pi` on `PATH`, so the name alone is a false positive.

**Alternatives rejected.**
- Honouring `PI_CODING_AGENT_DIR`: every destination is relative to the home, and D676 refused a
  provider home override for that reason (D755 handles Claude's directory by declaration). Paths
  are `$HOME/.pi/agent`; a symlink serves another account.
- Project `.pi/*` and `.agents/skills` cells: both load only after project trust (probed).
- Suppressing the `.agents/skills` copy a trusted project already gets from codex/copilot/antigravity
  projections: same content, benign; recorded as a watch item.
- Encoding `${VAR}` like Crush D225: it breaks the token header Pi itself interpolates.

**Consequences.** Pi-specific facts live in `docs/harnesses.md` §pi with the four probes. A user who
changes an entry's exposure in `/mcp` has it rewritten to `direct` by the next sync. Plan 2 adds the
`hook` cell through a generated extension.
