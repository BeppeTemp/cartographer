# Probe recipes

Every probe runs from a fresh temp directory unrelated to any project
(`mktemp -d`), with no custom `--agent`, and cleans up after itself. A probe is a
**sentinel plus a negative control**: the artifact under test appends a unique
token to a sentinel file; the control is the same run without the artifact (or
with a deliberately invalid one) and must leave the sentinel untouched. Record the
**mode matrix** (default engine, opt-in engine, interactive, non-interactive):
one version can behave differently per mode. Use only names the client documents
or accepts; an unknown trigger or key may be silently ignored rather than reported.
A probe that needs an install, upgrade or login stops and asks the operator.

Probe the global location and the project-local one separately; a matrix cell is
one or the other.

## Kiro

Run the real binary, not the Homebrew symlink (it breaks the launcher):
`KIRO="/Applications/Kiro CLI.app/Contents/MacOS/kiro-cli"` on macOS. Version:
`brew info --cask kiro-cli`. Kiro 2.2x carries three agent engines
(`--agent-engine v1|v2|v3`, default `v2`); a feature present only in a
non-default engine is not "supported" for a user who runs the default.

**Hook probe** (seeded from #265, Kiro CLI 2.26.1, KAS 0.66.15, 2026-10-01;
re-run on 2.27.0 for D300, 2026-10-02).

1. Write a `"version": "v1"` hook file with a `command` action that appends a
   token to `$SENTINEL`, once to `$HOME/.kiro/hooks/` and once to the workspace
   `.kiro/hooks/` (separate tokens). Triggers: `SessionStart`, `UserPromptSubmit`,
   `PreToolUse`, `Stop`, `SessionEnd`. Valid KAS triggers: `PreToolUse`,
   `PostToolUse`, `SessionStart`, `Stop`, `UserPromptSubmit`, `PreTaskExec`,
   `PostTaskExec`, `PostFileCreate`, `PostFileSave`, `PostFileDelete`, plus
   `SessionEnd` and `Manual`. `AgentSpawn` and `PromptSubmit` are **not** among
   them: do not use them as the positive case.
2. Run the mode matrix:
   - `"$KIRO" chat` interactive, default engine;
   - `"$KIRO" chat --no-interactive --agent-engine v2`, and `--agent-engine v3`;
   - `"$KIRO" chat --no-interactive --v3` with a prompt that makes a tool call;
   - `"$KIRO" chat --v3 --tui` interactive.
3. Interactive runs need a pty: `script -q "$LOG" "$KIRO" chat --v3 --tui` with
   scripted input, then exit cleanly. Wait ~20 s before typing: a prompt typed
   while the TUI shows `Initializing` is queued and may never be sent, and the
   `SessionStart` hook then never fires. That looks like a negative result and
   is not one.
4. Read the client's own logs, not only the sentinel:
   `grep -ri hook "$HOME/.kiro/logs/<run>/"`. Expected loader lines when it works:
   `v2 hooks cache initialized`, `loaded N standalone hooks from .kiro/hooks/`.
5. Negative control: the same run with the hook files removed.

Result on 2.26.1 and 2.27.0 (D300):

| Session | Standalone `~/.kiro/hooks/*.json` | Agent `hooks` (`agentSpawn`) |
|---|---|---|
| `chat` interactive (default; a new TUI over v2 since 2.27.0) | no | yes |
| `chat --no-interactive`, no `--agent`, with `chat.defaultAgent=<x>` | no | yes |
| `chat --v3 --tui` | yes (`SessionStart`, `UserPromptSubmit`, `Stop`) | yes |
| `--v3` / `--agent-engine v3`, non-interactive | no (the KAS hook cache is built only when the ACP `initialize` sends `hooks: {enabled: true, v2: true}`) | — |

The standalone loader does not recurse: a `*.json` in a subdirectory of
`~/.kiro/hooks/` is not loaded (`loaded N standalone hooks` counts only the top
level). The file schema is `{version: "v1", hooks: [{name, description?, trigger,
matcher?, action: {type: "command", command}, timeout? (seconds, default 60),
enabled?}]}`, with at least one entry. `PreToolUse` was not exercised.

**Default-agent probe** (D300; a negative result, recorded so the next audit does
not redo it): put `$HOME/.kiro/agents/<x>.json` with only `name`, `description`
and `hooks`, then compare `/tools` in an interactive `chat --agent <x>` against
`chat --agent kiro_default`. On 2.27.0 the bare agent has **no tools**, against
14 for the default. With `"tools": ["*"]` it has the same 14, but it still lacks
the default's built-in prompt. Side effect: running a session on an old-format
agent file leaves `<x>.json.bak` and writes `chat.enableAutoAgentUpgrade: true`
into `$HOME/.kiro/settings/cli.json`. Back up `cli.json` before the probe and
restore it byte for byte afterwards. Kiro also creates
`$HOME/.kiro/agents/agent_config.json.example`; delete it if it was not there
before.

**Skill probe**: place `<dir>/<name>/SKILL.md` in `$HOME/.kiro/skills/` and in the
workspace `.kiro/skills/`; confirm the client lists it (a symlinked skill dir must
load too; a materialised text file in its place loads nothing, silently).

**Agent probe**: `"$KIRO" agent list` must report a JSON agent in
`$HOME/.kiro/agents/` as `Global` and one in the workspace as `Workspace`
(workspace wins). A Markdown agent is not discovered (D195).

**MCP probe**: server in `$HOME/.kiro/settings/mcp.json`; confirm it is listed
and its tools load.

**Instructions probe**: `$HOME/.kiro/steering/cartographer.md` containing a
unique token; ask the client to repeat it.

## Claude Code

- Skill: `$HOME/.claude/skills/<name>/SKILL.md`; the skill appears in the
  session's skill list; negative control without the file.
- Agent: `$HOME/.claude/agents/<name>.md`; listed under that name (frontmatter
  `name` must equal the file name).
- Hook: `SessionStart` entry in `$HOME/.claude/settings.json` appending to the
  sentinel; negative control without the entry.
- MCP: server in `$HOME/.claude.json` (`mcpServers`) or project `.mcp.json`;
  listed and connected.
- Instructions: token in `$HOME/.claude/CLAUDE.md` (project: `CLAUDE.md`), asked
  back.
- Project `AGENTS.md` (since 2.1.277): token in `AGENTS.md` of a temp git repo,
  asked back with `claude -p`, then again with a `CLAUDE.md` holding only the
  managed block (expected: not read) and with `@AGENTS.md` added to it (read).
  The `agents-md@builtin` plugin can be off on the probing machine; test the
  default with `--settings '{"enabledPlugins":{"agents-md@builtin":true}}'`.

## Codex

- Skill and agent discovery: `codex debug prompt-input` lists the catalogued
  skills; check `$HOME/.codex/skills/` (global, D192) and project
  `.agents/skills/` (D193). The client's own discovery output is what
  `internal/provisioning/clientcompat_test.go` asserts: run it with the client
  installed.
- Agent: TOML in `$HOME/.codex/agents/`; listed under its `name`.
- Hook: entry in `$HOME/.codex/hooks.json`, sentinel plus negative control. Hook
  trust is keyed by file, and a project's `.codex/` layer is inactive unless the
  project is trusted.
- MCP and instructions: `$HOME/.codex/config.toml`, `$HOME/.codex/AGENTS.md`.

## OpenCode

- Discovery: `opencode agent list` and `opencode debug config` list agents,
  skills and MCP servers as the client sees them (D192, `clientcompat_test.go`).
- Agent: `$HOME/.opencode/agent/<name>.md` (documented path is
  `.opencode/agents`; check both on a new release).
- Hook: the generated plugin in `$HOME/.config/opencode/plugins/` fires on
  `session.created`; sentinel plus negative control.
- MCP: `opencode.json`; instructions: `$HOME/.config/opencode/AGENTS.md`.

## Hermes

- Skill: delivered to `$HERMES_HOME/skill-inbox/<name>/cartographer/` with a
  generated `SOURCE.md`; probe that the agent can adopt it through its own
  `skill_manage` tool, and that `$HERMES_HOME/skills/` is **untouched** (negative
  control).
- A new native skill, agent or hook mechanism is `cell`-class: probe it before
  proposing to flip an unsupported cell.

## Antigravity

- Everything is global under `$HOME/.gemini`: skill `config/skills`, agent
  `config/agents`, hook `config/hooks.json`, MCP `config/mcp_config.json`,
  instructions `GEMINI.md`. Probe each with a token or sentinel as above.
- Watch for a project-local configuration root (flips the D193 cells) and for a
  `SessionStart` event (changes the sync layer-1 trigger).

## Crush

- Skill: `$HOME/.config/crush/skills/` (project `.crush/skills/`); listed by the
  client.
- MCP: `$HOME/.config/crush/crush.json`; instructions:
  `$HOME/.config/crush/CRUSH.md`.
- Watch for a documented subagent directory, a hook mechanism, or `crushrc` becoming
  the only supported config (D225).
