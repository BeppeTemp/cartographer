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

Each pass also checks **MCP tool visibility** per client: code mode, tool search,
the keys that pin a tool or server, and how server instructions are shown.

Probe the global location and the project-local one separately; a matrix cell is
one or the other.

## Write-findings probe (D353, any client)

Seeded from #637–#640 (2026-10-09). The question is three-fold: does a
`PostToolUse` hook fire for an MCP write, does its payload carry the tool
result, and which output channel reaches the model **without** making the agent
believe the write failed.

1. Server: `cartographer kb create kb --no-remote --local --data <tmp>` once, then the client's MCP
   entry runs `cartographer serve --kb <tmp>/kb` (stdio). A `concept_write` whose
   body links to a missing page returns findings (`broken_link`, `orphan`).
2. Hook: a script that saves stdin to a file, logs its argv, and answers per a
   mode file: `2` (stderr, exit 2), `0` (stderr, exit 0: the negative control),
   `ctx` (`{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":…}}`
   on stdout, exit 0), `out` (plain stdout), `block` (`{"decision":"block",…}`).
   The message carries a random token. Register it twice: no matcher (to learn
   the tool name) and Claude's matcher.
3. Prompt: "call concept_write … then answer `RESULT=` followed by any secret word
   a hook told you, or NONE". Grep `RESULT=<token>`; count the hook calls (a
   retried write doubles them).
4. Feed a saved payload to `cartographer hook write-findings` to see whether the
   parser needs a change.

Outcomes on 2026-10-09: codex `ctx` (exit 2 replaces the result, the agent
retries); opencode 2.x, plugin mutating `event.result.output` (throw = retry);
kiro none; antigravity none (no result in the payload). Per-client detail in
`docs/harnesses.md`.

## Kiro

Run the real binary: on 3.0.0 the Homebrew symlink works, and the binary is
`/opt/homebrew/Caskroom/kiro-cli/<v>/kirocli/bin/kiro-cli` (on 2.x the symlink
broke the launcher; the app bundle was `KIRO="/Applications/Kiro CLI.app/Contents/MacOS/kiro-cli"`).
`kiro-cli agent upgrade --dry-run` works without login. Version:
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

**MCP shadow probe** (same server name in both files; a KB `mcp` artifact in
workspace scope lands in the project `.kiro/settings/mcp.json` next to the
global `cartographer` entry). The server is a local stdlib script, never a
fetched package: a `python3 -I` script that answers `initialize` and
`tools/list` with one tool whose name it takes from `argv[1]`. In a temp
workspace (`mktemp -d`), `.kiro/settings/mcp.json` declares `probe` running it
with `probe_project`. The global file is `$HOME/.kiro/settings/mcp.json`: try a
scratch `HOME` first; if `mcp list` asks for a login there (it does on Kiro CLI
3.0.0), ask the operator, then back it up (`cp -p`), add a `probe` entry running
the script with `probe_global`, and after the runs restore the backup and check
`shasum` is identical. Runs, from the workspace: both files; global only (move
the project file away); project only (global restored). `"$KIRO" mcp list` lists
one merged server name without its source file, so read the tools from a chat:
`"$KIRO" chat --no-interactive --trust-workspace --trust-tools= "Without calling
any tool: list the names of your tools containing probe."` (`--trust-tools=`
trusts none: no tool runs). Each single-file run is the negative control (its
tool must show up). Record which tool the both-files run lists. Delete the temp
workspace.

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
  `internal/provisioning/contract/clientcompat_test.go` asserts: run it with the client
  installed.
- Agent: TOML in `$HOME/.codex/agents/`; listed under its `name`.
- Hook: entry in `$HOME/.codex/hooks.json`, sentinel plus negative control. Hook
  trust is keyed by file, and a project's `.codex/` layer is inactive unless the
  project is trusted.
- MCP and instructions: `$HOME/.codex/config.toml`, `$HOME/.codex/AGENTS.md`.

## OpenCode

- Discovery: `opencode debug paths` reports the config directory the global
  skills and agents must sit under (D360, `clientcompat_test.go`); `opencode
  debug agents` lists agents (2.x needs `opencode reload` first). `opencode agent
  list` no longer exists on 2.x.
- Background service (2.x): any non-`--standalone` command (`debug agents`,
  `reload`, the TUI) spawns a detached `opencode serve --service` that outlives the
  probe, on the default port 49374 unless `service.json` sets one. Under a temp
  `XDG_*` it squats the operator's port and their `opencode` fails with "Managed
  service port … already in use" (2026-11-05). Before the first command run
  `opencode service set port <free port>` with the probe's `XDG_*`; at cleanup run
  `opencode service stop` with the same environment, then check that
  `pgrep -f 'opencode serve --service'` shows no probe PID.
- Skill and agent: `$HOME/.config/opencode/skills/<name>/` and `agents/<name>.md`.
- Hook: the generated plugin in `$HOME/.config/opencode/plugins/` fires on
  `session.created`; sentinel plus negative control.
- MCP: `$HOME/.config/opencode/opencode.json` (`opencode debug config` from a git
  repository shows the merged result); instructions: `$HOME/.config/opencode/AGENTS.md`.
- A new native skill, agent or hook mechanism in any client is `cell`-class: probe it before
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

## Copilot CLI

- Always set `COPILOT_HOME` to a scratch directory; never touch `$HOME/.copilot`.
- Skill, instructions and MCP need no model: `copilot skill list`,
  `copilot instruction list`, `copilot mcp list` report the cell ("Personal skills",
  "Personal instructions", "User servers"). Hooks and agents need
  `copilot -p "<prompt>" -s --allow-all-tools`.
- Global cells under `$COPILOT_HOME`: `mcp-config.json`, `copilot-instructions.md`,
  `skills/`, `agents/<name>.agent.md`, `hooks/<file>.json`.
- Watch for the project mcp, agent and hook cells working without folder trust
  (flips the three unsupported project cells) and for a hook matcher (D676).
- Copilot in VS Code is a separate recipe (`## Copilot in VS Code`).

## Copilot in VS Code

Probed on VS Code 1.145.2 (2026-11-09): the chat runs the embedded Copilot CLI runtime
(`session.start` says `producer: copilot-agent`), so the cells are the CLI's.

- Launch VS Code with `COPILOT_HOME=<scratch>` on a scratch git workspace the operator
  trusts; never touch `$HOME/.copilot` (snapshot `find … | shasum` before and after).
  A fresh `--user-data-dir` needs a GitHub sign-in, so reuse the signed-in profile.
- Send prompts with `code chat -r -m <mode> "…"` (opens the Copilot harness; the Local
  harness cannot be chosen from the command line); read the answer from
  `<scratch>/session-state/<id>/events.jsonl`.
- Seed the global cells as in §Copilot CLI and the workspace cells
  (`.github/agents`, `.github/hooks`, `.github/skills`, `.agents/skills`, `AGENTS.md`,
  `.github/copilot-instructions.md`, `.mcp.json`), each with a unique token.
- Observed: all five global cells; `.github/agents` and `.github/hooks` in a trusted
  workspace; both skill directories; `.mcp.json` not loaded with default settings.
  Not run: untrusted control, Local harness, hook matcher and `deny`.

## Pi

- Always set `PI_CODING_AGENT_DIR` to a scratch directory (copy only `auth.json` from
  `$HOME/.pi/agent` when a model is needed); never touch `$HOME/.pi/agent`. Cartographer itself
  ignores the variable, so a probe of a Cartographer-written cell reads from `$HOME`.
- MCP needs no model: `pi mcp list` shows the entry, its exposure and its tool count.
- Skills, instructions and extensions need a session:
  `pi -p --no-session --model <provider/id> [-a|-na] "<prompt>"` (`-a` trusts the project, `-na`
  does not). `pi --version` prints a bare `MAJOR.MINOR.PATCH`; detection relies on it.
- Global cells under the agent dir: `mcp.json`, `AGENTS.md`, `skills/<name>/`, `extensions/*.ts`.
- Watch for project `.pi/*` and `.agents/skills` loading without trust (flips the unsupported
  project cells), the `exposure` default, and the `CLAUDE.md` global fallback order (unprobed).

## Cline

- Probe with a scratch `CLINE_DIR` and `CLINE_MCP_SETTINGS_PATH`; never touch `$HOME/.cline`.
  Cartographer ignores both variables, so a probe of a Cartographer-written cell reads from `$HOME`.
- The cells come from the extension bundle (`saoudrizwan.claude-dev`, `dist/extension.js`), not from
  vendor docs: re-read the MCP settings parsing, the rules loader (single loader for `.cline/rules` and
  `Documents/Cline/Rules`, no conditional frontmatter), the agent loader (`.yaml`/`.yml`, non-empty body)
  and the skills directories.
- Watch for MCP `roots` (flips every unsupported project cell), a per-workspace MCP file, `${VAR}`
  expansion in `headers` (flips `SupportsMCPHeaders`), a rename of the extension id (detection), and
  whether the `cline` CLI shares `$HOME/.cline`.
- Hooks: probe with a scratch `Documents/Cline/Hooks/<Event>` that appends its stdin to a file, never the
  real hooks. P1: does `TaskStart` fire once per task or once per turn. P2: the `toolName` and `result` of
  a Cartographer write response (`result` is `String(output)`; the write-findings hook is claimed for Cline
  only when it is the response text). P3: a `PreToolUse` hook printing `{"cancel":true,"errorMessage":"x"}`
  stops the task and shows the message. Once on Windows: `<Event>.ps1` run by `powershell -NoProfile
  -NonInteractive -ExecutionPolicy Bypass -File`.

## Capability probes (D1656)

One recipe per client; its name is what a `verified` line of the `docs/harnesses.md` capability block
cites. Common shape: a fresh `mktemp -d` (git repository inside), a KB made with `cartographer kb create kb
--no-remote --local --data <tmp>/data` for the MCP entry (`cartographer serve --kb <tmp>/data/kb`), a skill
`probe-skill` whose description asks for the token `SKILLTOK-7731` (its frontmatter carries
`requires: [skill:other]`, which is the unknown-key probe), an agent `probe-agent` whose description says it
knows "the zebra code" and whose body answers `ZEBRA-4412`, and one hook per event appending `H-<Event>` to a
sentinel file. Each cell is run once **with** the artifacts and once **without** (the negative control:
`NONE`, empty sentinel). `agent.delegate` is the prompt "find the zebra code by delegating to a subagent if
one fits" (the model chooses the agent from its description; the parent's Read/Grep/Bash are denied, or the
body would be found in the file); `agent.invoke` is "use the probe-agent agent to get the code". Use
non-interactive runs with a short prompt and an alarm. Ancestor probe: the same artifact in a non-git `W`,
asked from `W/repo` (a git repository) and, as the positive control, from `W`.
Safety: create every scratch dir with `mktemp -d` (mode 0700) and remove them at the end, copied
credentials (`auth.json`, `settings.json`) included. A recipe that trusts all tools without a scratch `HOME`
(`kiro-project`) is run from a `cd` into the temp project only, with no global cell, or after asking the operator.

- **claude-project**: artifacts under the temp dir's `.claude/{skills,agents,settings.json}` and `.mcp.json`;
  `claude -p --model haiku --disallowedTools=Read,Glob,Grep,Bash --allowedTools=Agent,Task "<prompt>" </dev/null`
  (the `=` form: a space makes the variadic flag swallow the prompt). An agent whose `tools:` names a tool the
  parent denies is refused as "invalid tools": leave `tools` out. MCP: ask for the count of `mcp__probe__*`
  tools; a user setting that auto-approves project servers makes `.mcp.json` load with no prompt, so the
  control is the same prompt in a dir with no `.mcp.json`. The model may decline to delegate to an agent whose
  description claims knowledge it cannot verify: `agent.delegate` stays unverified until it does.
- **codex-scratch-home**: scratch `HOME` and `CODEX_HOME` with only `auth.json` copied; global cells under
  `$CODEX_HOME/{skills,agents,hooks.json,config.toml}`, project cells under `.agents/skills`, `.codex/` after
  `[projects."<dir>"] trust_level = "trusted"` in the scratch config. `codex exec --skip-git-repo-check
  --dangerously-bypass-hook-trust -s read-only "<prompt>" </dev/null`. MCP: `[mcp_servers.probe]`, ask for the
  tool count (`codex mcp list` shows only that the entry is enabled; the global MCP cell was not probed).
- **opencode-scratch-xdg**: scratch `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME` (the free
  `opencode/big-pickle` model needs no credentials); `opencode run --standalone --auto -m opencode/big-pickle`.
  Skills `.opencode/skills/`, agents `.opencode/agents/` **and** `.opencode/agent/` (both load on 2.2.5), hooks
  as plugins `.opencode/plugins/*.js` in the 2.x shape (`ctx.tool.hook("execute.before"|"execute.after")`,
  `ctx.event.subscribe` filtered on `session.created`/`session.idle`), MCP in `opencode.json`;
  `opencode mcp list` and `opencode debug agents --standalone` (the background service caches and its port may
  be taken by the real one). `session.idle` never fired in `run`: `Stop` stays unverified.
- **kiro-project**: real `$HOME` (Kiro keeps its login there), project cells only: `.kiro/skills/`,
  `.kiro/agents/<name>.json` (`tools: ["*"]`), `.kiro/settings/mcp.json`; `kiro-cli chat --no-interactive -a
  --trust-workspace "<prompt>"`. The transcript prints `Load skill: <name>` and `Sub-agent: <name>`. MCP:
  `kiro-cli mcp list` shows the entry under the default agent (a model-side count is useless, the real global
  config carries the same tools). Global cells are not probed: they would write into `~/.kiro`.
- **pi-scratch-agent-dir**: `PI_CODING_AGENT_DIR` scratch with `auth.json` and `settings.json` copied;
  `pi -p --no-session "<prompt>"`. Skill in `skills/`, MCP in `mcp.json` (`pi mcp list`, then the count in a
  session), hooks as one extension `extensions/*.ts` listening on `session_start`, `tool_call`, `tool_result`.
