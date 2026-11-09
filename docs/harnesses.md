# Harness ledger

Per agent client ("harness"), the client version Cartographer was last aligned
with, where that client announces changes, and which parts of the provider matrix
depend on it. The matrix itself stays the source of truth for *what* is written
where (`destinationMatrix` in `internal/provisioning/provisioning.go`,
`projectDestinationMatrix` in `internal/provisioning/workspacescope.go`, the
descriptors in `internal/configurator/registry.go`); this page records *how
recently it was checked* and *what to watch*. The `cartographer-audit-clients` skill
(`.claude/skills/cartographer-audit-clients/SKILL.md`) reads it as its baseline and updates it
as its output ([D292](decisions/D292-a-harness-ledger-and-a-procedure-not-version-polling.md)).

The providers a plugin declares are a fork of one of the clients below (its `base`): they get the destinations the plugin's descriptor names, never an entry in the matrices or in this ledger, and their capability rows stay `absent` or `unverified` until probed (see [plugins](plugins.md)).

`TestHarnessLedgerCoversEveryProvider` requires one `## <provider-id>` section per
provider in `configurator.Providers()` and no section for an unknown one.

Each section also carries a fenced `capabilities` block, the ledger of the per-client capability
table (`internal/provisioning/capabilities.go`, D1656): one line `<scope> <id> <status>` for every
scope (`global`, `project`) and id (`skill`, `mcp`, `agent.delegate`, `agent.invoke`,
`hook.<Event>` for each event of the vocabulary). `absent` is derived from the destination matrix
and the hook vocabulary, so a cell with no destination is `absent`; `unverified` is the default for
the rest; `verified` is written `<scope> <id> verified <YYYY-MM-DD> <recipe>`, the recipe a name in
`probes.md`, and only a probe earns it. `TestCapabilityTableMatchesLedger` fails on any difference
between the block and the Go table, on a provider without a block, and on a `verified` line without
date and recipe: change the table and the block in the same PR.

**Capability probes (2026-11-05, recipes in `probes.md` §Capability probes).** Claude Code 2.1.318,
Codex CLI 0.166.0, Kiro CLI 3.0.0, OpenCode 2.2.5, Pi 1.5.0; Cline, Crush, Copilot CLI and Antigravity were
not probed and stay `unverified`. A skill whose frontmatter carries the unknown key `requires` loaded on all
five (negative control: no skill, `NONE`), and an agent with `requires` in its frontmatter was invoked on
Claude Code only: no probed client refuses or mangles it, so none needs `requires` stripped from a skill copy.
Codex TOML, Kiro JSON and OpenCode agent files with `requires` are unprobed. Agents and
hooks placed in a non-git folder `W`, asked from a git repository `W/repo` (the positive control from `W`):

| Client (version) | agent in `W/repo` | hook in `W/repo` |
|---|---|---|
| Claude Code 2.1.318 | no | no |
| Codex 0.166.0 | no | no |
| Kiro CLI 3.0.0 | no | not probed |
| OpenCode 2.2.5 | yes | not probed |

Conventions: `unknown` means no version or date was ever recorded in this
repository; `unverified` means a source was named but not confirmed to exist. A
value is never filled in from memory. Documentation is not evidence: a change that
would flip a matrix cell is confirmed with a probe
(`.claude/skills/cartographer-audit-clients/references/probes.md`), not from a changelog line.

**Project skills and instructions below a bound folder (probe 2026-10-21, D1019).** A project
skill and project instructions in a bound non-git folder `W`, asked back from `W`, from a git
repository `W/repo` and from a plain subfolder `W/plain`. A client that looks for project
skills only up to the repository root gets, from a folder binding, the repositories below it as
tools (through the bridge, D737) but not their skills or agents: a repository a KB declares in its
`paths.yaml` is therefore bound itself and projected into (`sync.md` §Workspace scope).

| Client (version) | skill in `W/repo` | instructions in `W/repo` |
|---|---|---|
| Claude Code 2.1.304 | no | yes (`CLAUDE.md` is read up every ancestor) |
| Codex 0.163.0 | no | no |
| Copilot CLI 1.0.98 | no | no |
| Kiro CLI 2.32.0 | no | no |
| OpenCode 2.0.32 | yes | no |

Claude Code 2.1.304 also reads an untracked `CLAUDE.local.md` beside a tracked `CLAUDE.md`
(probed 2026-10-22: a unique token in it was repeated by `claude -p`), which is where a derived
binding puts its instructions block when the repository tracks `CLAUDE.md`.

## claude

- **Aligned with**: `2.1.318` — 2026-11-06, documentary review of the MCP tool visibility: MCP tools are
  deferred behind tool search by default; per-tool `_meta["anthropic/alwaysLoad"]` and per-server
  `alwaysLoad` pin them; server instructions are loaded at start, capped at 4 096 chars.
  `2.1.286` — 2026-10-01. Documentary review of the changelog
  from 2.1.246 (about 40 releases) and of the vendor docs; one cell probed (the
  project `CLAUDE.md` vs `AGENTS.md` precedence, below), the others not re-probed.
- **Sources** (reachable on 2026-10-01): changelog
  <https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md>;
  version: `claude --version`, latest `npm view @anthropic-ai/claude-code version`
  (dist-tags `latest`, `next`, `stable`); docs <https://code.claude.com/docs/en/memory>,
  `/sub-agents`, `/skills`, `/hooks`, `/mcp`, `/settings` under the same prefix.
- **Depends on**:
  - MCP tool hiding (D1724; read 2026-11-06, Claude Code 2.1.318, not probed in a session; read in the installed 2.1.318 binary): tools are deferred behind tool search. The binary builds an MCP tool with `alwaysLoad` true when the tool's listed `_meta["anthropic/alwaysLoad"] === true` (and the tool is not otherwise excluded), and a server-level `alwaysLoad` config key exists beside it, so the per-tool pin is honoured by the code; that a real session shows the tools declared is not yet observed (#1714). The server lists its entry tools (`atlas_overview`, `search`, `concept_read`, `graph_context`) with `_meta["anthropic/alwaysLoad"]: true`, which keeps them declared; the rest stay deferred. `initialize.instructions` (loaded at session start, 4096 characters) carries the entry paragraph on a provisioned connection.
  - Stdio bridge (D737): `cartographer mcp --provider claude` is spawned in the session's folder (probe 2026-10-17, Claude Code 2.1.302: working directory yes, `roots` declared with `[cwd]`). The token variable reaches the child (the process environment is inherited). Replaces the D717 project-entry shadowing, which is no longer used.
  - Writer header (D904): an HTTP entry carries `Cartographer-Author` in `headers`, next to `Authorization`.
  - `skill`/`agent`/`hook` in `~/.claude/{skills,agents,hooks}/`, project-local in
    `.claude/…` (D137, D193); `mcp` in `~/.claude.json` (project: `.mcp.json`);
    `instructions` in `~/.claude/CLAUDE.md` (project: `CLAUDE.md`).
    Under `CLAUDE_CONFIG_DIR` only the `cartographer` MCP entries follow `providers.claude.config_dirs`
    (D755); everything else here stays `$HOME`-relative (symlink it per account).
  - Agent translation is a passthrough: Cartographer subagents are Claude
    subagents; Claude takes its own restriction fields (`tools`, also
    `disallowedTools`, `permissionMode`) at the top level (D291).
  - Sync layer 1: `SessionStart` entry in `~/.claude/settings.json`
    (`docs/sync.md` §Layer 1); hooks run through Git Bash on Windows, PowerShell
    as the documented fallback (D267).
  - Write-findings feedback (D353): a `PostToolUse` entry (matcher on the MCP
    tool name) whose command exits 2 with a message on stderr returns that
    message to the agent. Payload: `tool_name` and `tool_response`. The other
    clients were probed on 2026-10-09 (#637–#640): codex and opencode carry the
    findings through a different channel (their sections), antigravity and kiro
    cannot carry them at all.
  - `CLAUDE.md` → `@AGENTS.md` import and per-directory loading (D213). Since
    2.1.277 the built-in `agents-md@builtin` plugin (on by default) reads a
    project `AGENTS.md` itself, but **only when no `CLAUDE.md`/`CLAUDE.local.md`
    is on the project path**; the import stays necessary wherever a `CLAUDE.md`
    exists.
  - session transcripts, read by the usage scan (D326): `~/.claude/projects/**/*.jsonl`,
    one JSON record per line; `assistant` records carry `message.content` blocks, and
    the scan reads only `tool_use` blocks (the `Skill` tool's `input.skill`, the
    `Agent`/`Task` tool's `input.subagent_type`, and any other tool whose arguments
    carry a materialised skill path). The format is undocumented: an unknown shape is
    skipped, never fatal.
- **Watch items**:
  - the `PostToolUse` payload shape of an MCP result (`tool_response`) is not
    documented to the byte: the write-findings hook accepts the result object, a
    content-block array and JSON in a text block, and is silent on any other
    shape, so a change shows up as the hook going quiet.
  - the project `instructions` block prepends `@AGENTS.md` when the project root
    has one, because a project `CLAUDE.md` hides the repository's `AGENTS.md` from
    the built-in plugin otherwise ([D293](decisions/D293-claude-md-imports-agents-md.md)).
  - claude.ai account-synced skills share `~/.claude/skills/` (2.1.275, 2.1.280);
    not a cell.
- **Probe notes**: the AGENTS.md probe needs the plugin on: a machine can disable
  it (`"agents-md@builtin": false` in `~/.claude/settings.json`), so pass
  `--settings '{"enabledPlugins":{"agents-md@builtin":true}}'` to `claude -p` to
  test the default. Ask for a token planted in the file under test.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill unverified
global mcp unverified
global agent.delegate unverified
global agent.invoke unverified
global hook.PostInvocation absent
global hook.PostToolUse unverified
global hook.PreCompact unverified
global hook.PreInvocation absent
global hook.PreToolUse unverified
global hook.SessionStart unverified
global hook.Stop unverified
global hook.SubagentStop unverified
global hook.UserPromptSubmit unverified
project skill verified 2026-11-05 claude-project
project mcp verified 2026-11-05 claude-project
project agent.delegate unverified
project agent.invoke verified 2026-11-05 claude-project
project hook.PostInvocation absent
project hook.PostToolUse verified 2026-11-05 claude-project
project hook.PreCompact unverified
project hook.PreInvocation absent
project hook.PreToolUse verified 2026-11-05 claude-project
project hook.SessionStart verified 2026-11-05 claude-project
project hook.Stop verified 2026-11-05 claude-project
project hook.SubagentStop verified 2026-11-05 claude-project
project hook.UserPromptSubmit verified 2026-11-05 claude-project
```

## codex

- **Aligned with**: `0.166.0` — 2026-11-06, MCP tool visibility: recent models run
  `tool_mode: code_mode_only`; MCP tools are always deferred behind tool search; there is no per-tool pin
  (`code_mode.direct_only_tool_namespaces` is whole-namespace).
  `0.162.0` — 2026-10-09 for the write-findings probe (#637,
  below); `0.159.3` — 2026-10-01 (documentary review). Probed on
  `0.158.0` — 2026-10-01: `codex debug prompt-input` catalogues skills from both
  `~/.codex/skills` and `~/.agents/skills` (D192 still holds); a user-layer
  `SessionStart` hook fires in `codex exec`, but only for a trusted hook
  definition; a project's `.codex/hooks.json` runs only once the project is
  trusted (D193 still holds). Not probed: `sandbox_mode` on an agent.
- **Sources**: changelog <https://learn.chatgpt.com/docs/changelog>; releases
  <https://github.com/openai/codex/releases> (tags `rust-v<version>`); version:
  `npm view @openai/codex version`, `codex --version`. Documentation (the
  `developers.openai.com/codex/...` URLs redirect here):
  <https://learn.chatgpt.com/docs/build-skills>,
  <https://learn.chatgpt.com/docs/agent-configuration/agents-md>,
  <https://learn.chatgpt.com/docs/agent-configuration/subagents>,
  <https://learn.chatgpt.com/docs/hooks>,
  <https://learn.chatgpt.com/docs/extend/mcp?surface=cli>.
- **Depends on**:
  - MCP tool hiding (D1724; read 2026-11-06, Codex 0.166.0): recent models run `code_mode_only` and MCP tools are always deferred behind tool search. There is no per-tool pin (only `code_mode.direct_only_tool_namespaces`, a whole-server opt-out Cartographer does not write): the entry tools carry the vendor `_meta` keys for other clients and Codex relies on the entry paragraph of `initialize.instructions` (whether it shows them is not established). Upstream request to file.
  - Stdio bridge (D737): spawned in the session's folder with `$PWD` unset (probe 2026-10-17, Codex 0.163.0); `roots` not declared. Codex filters the environment of an MCP server, so the entry lists the token variable in `env_vars`; every other client inherits it.
  - Writer header (D904): an HTTP entry carries it in `http_headers`, which Codex sends as written (config reference; not probed). Auth stays `bearer_token_env_var`.
  - `skill` global `~/.codex/skills/` (deliberate divergence from the documented
    `$HOME/.agents/skills`, still the only documented user path, D192, guarded by
    `clientcompat_test.go`); project `.agents/skills/` (D193).
  - `agent` as TOML (`name`/`description`/`developer_instructions`) in
    `~/.codex/agents/` (project: `.codex/agents/`); `sandbox_mode` is a documented
    optional agent field (D291: documented, not probed).
  - `hook` files in `~/.codex/hooks/`, registered in `~/.codex/hooks.json` (D230;
    `config.toml` and project `.codex/hooks.json` are also documented); sync
    layer 1 `SessionStart`.
  - `mcp` and `instructions` in `~/.codex/config.toml` and `~/.codex/AGENTS.md`
    (`AGENTS.override.md` in the same directory wins, first non-empty file only);
    hook trust is keyed by the exact hook definition, and a project's `.codex/`
    layer is inactive unless the project is trusted (D193); `workspace bind
    codex … --trust` writes the `[projects."<path>"]` table on request (D1074).
  - `PostToolUse` write-findings hook (D361), registered in the same `hooks.json`.
  - write-findings feedback (D353, probed on 0.162.0, #637): a user-layer
    `PostToolUse` entry with Claude's matcher (`mcp__.*__(concept_write|…)$`)
    fires for an MCP tool named `mcp__<server>__<tool>`; the payload carries
    `tool_name` and `tool_response` as the MCP result object (`content` blocks),
    which `cartographer hook write-findings` already parses. **Exit 2 is the wrong
    channel here**: Codex reports `PostToolUse Blocked`, replaces the tool result
    with the stderr text, and the agent, seeing a failed call, repeats the write.
    `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"…"}}`
    on stdout with exit 0 reaches the model and keeps the result; exit 0 with
    stderr only does not reach it (negative control). **Supported** (D361): the
    hook is installed with `--channel context`; the user approves it once.
  - session transcripts, read by the usage scan (D326):
    `~/.codex/sessions/**/rollout-*.jsonl`. Codex records no per-activation skill event:
    a `world_state` event whose `payload.state.host_skills` is non-empty proves the
    catalogue was loaded (a catalogue sighting, not a use), and a `response_item` tool
    call (`function_call`, `local_shell_call`, `custom_tool_call`, `mcp_tool_call`)
    whose arguments carry a materialised skill path is a use. Message and reasoning
    items are never read.
- **Watch items**: none open.
- **Probe notes**: `codex debug prompt-input` lists the catalogued skills (no
  model call). Isolate a run with a temp `HOME` and `CODEX_HOME`, copying only
  `auth.json`; close stdin (`</dev/null`) or `codex exec` waits for input. Hook
  trust is per definition (a `trusted_hash` under `[hooks.state]` in
  `config.toml`, granted interactively): an untrusted sentinel never fires, so
  probe with `codex exec --dangerously-bypass-hook-trust` inside the isolated
  home only. Project trust: `[projects."<path>"] trust_level = "trusted"`.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill verified 2026-11-05 codex-scratch-home
global mcp unverified
global agent.delegate verified 2026-11-05 codex-scratch-home
global agent.invoke verified 2026-11-05 codex-scratch-home
global hook.PostInvocation absent
global hook.PostToolUse verified 2026-11-05 codex-scratch-home
global hook.PreCompact unverified
global hook.PreInvocation absent
global hook.PreToolUse verified 2026-11-05 codex-scratch-home
global hook.SessionStart verified 2026-11-05 codex-scratch-home
global hook.Stop verified 2026-11-05 codex-scratch-home
global hook.SubagentStop unverified
global hook.UserPromptSubmit verified 2026-11-05 codex-scratch-home
project skill verified 2026-11-05 codex-scratch-home
project mcp verified 2026-11-05 codex-scratch-home
project agent.delegate verified 2026-11-05 codex-scratch-home
project agent.invoke verified 2026-11-05 codex-scratch-home
project hook.PostInvocation absent
project hook.PostToolUse verified 2026-11-05 codex-scratch-home
project hook.PreCompact unverified
project hook.PreInvocation absent
project hook.PreToolUse verified 2026-11-05 codex-scratch-home
project hook.SessionStart verified 2026-11-05 codex-scratch-home
project hook.Stop verified 2026-11-05 codex-scratch-home
project hook.SubagentStop unverified
project hook.UserPromptSubmit verified 2026-11-05 codex-scratch-home
```

## kiro

- **Aligned with**: `3.0.0` — 2026-11-06 (`3.1.0` seen, not in the changelog): V3 is the default; tool
  search is on with no pin (only the global `toolSearch.enabled`); workspace trust gates the project cells
  (2.33); V3 session store (#1716); agent auto-upgrade (#1717). `2.28.0` — 2026-10-09 for the write-findings probe (#640,
  below; `chat` without flags still runs the v2 engine and announces V3 as an
  early release); `2.27.0` — 2026-10-02 for the `hook` cell (D300: the default
  interactive TUI, `--no-interactive`, `--v3 --tui`, the default-agent probe);
  `2.26.1` — 2026-10-01 for the first hook re-probe (KAS `0.66.15`); `2.21.3` —
  2026-09-11 for the agent and hook cells (D195); `2.21.4` for skill symlinks
  (D207, `CONTRIBUTING.md`); `2.20.0` — 2026-08-27 for D140.
- **Sources**: changelog <https://kiro.dev/changelog/cli/>; hook docs
  <https://kiro.dev/docs/hooks/> and <https://kiro.dev/docs/cli/v3/hooks-migration/>
  (all cited in D195/D140); version: `brew info --cask kiro-cli`.
- **Depends on**:
  - MCP tool hiding (D1724; read 2026-11-06, Kiro 3.x): tools are loaded on demand (tool search); no per-tool pin found. Cartographer writes nothing for it; the entry paragraph of `initialize.instructions` is the only hint. Upstream request to file; the Kiro 3.x matrix is a separate audit.
  - Stdio bridge (D737): spawned in the session's folder (probe 2026-10-17, Kiro CLI 2.32.0); `roots` not declared. Kiro IDE not probed: an IDE that spawns the server outside the workspace and declares no roots gets the provider's default set.
  - Writer header (D904): none. Kiro documents `headers` for a remote server (<https://kiro.dev/docs/mcp/configuration/>), but the emitter writes no headers for Kiro, `Authorization` included (an older gap), so a write over a Kiro HTTP entry is authored by the token; the bridge names the writer.
  - `agent` as JSON (`name`/`description`/`prompt`) in `~/.kiro/agents/`,
    workspace `.kiro/agents/` wins over global (D195); native restriction keys
    believed to be `tools`/`allowedTools` (D291, not verified). The ledger names no
    read-only tool set, so the repository's read-only agents list `["*"]` and keep the
    restriction in the prompt; an agent with no `tools` has none (D300); sync warns per agent installed without a `providers.kiro` block (#1826).
  - Agent upgrade (#1717; probe 2026-11-06, Kiro CLI 3.0.0, scratch `HOME`, `kiro-cli agent upgrade <dir>`, no login): V3 rewrites a V2 agent that carries `tools`/`allowedTools`/`toolsSettings` in place (`--dry-run` reports "would upgrade"), keeps the original byte for byte as `<name>.json.bak`, adds `permissions` and a top-level `kiroUpgrade` block (`revision`, `fingerprint`, `source`), and leaves the V2 keys in the file. A plain `name`/`description`/`prompt` agent is not touched. Not probed: the session-start auto-upgrade itself, so the rewrite loop was reasoned from this and the `.bak` seen on real workspaces, not watched. A second upgrade does **not** overwrite an existing backup (probe 2026-11-06, Kiro CLI 3.0.0): the original of the next upgrade goes to `<name>.json.bak.1`, then `.bak.2`, and the first `.bak` stays as it was; an already-upgraded file is reported "already universal" and left byte for byte. Cartographer reads a file with `kiroUpgrade` whose `.bak` or `.bak.<n>` hashes to the lock's hash and keeps every other key but `permissions` as the backup's as not drifted, and binds the accepted file to `upgraded_hash` (`kiroUpgradeAccepted`), and `artifact_write` warns on `providers.kiro.allowedTools`/`toolsSettings`: the author writes the V3 `permissions` key instead (copied verbatim, D291). Watch: the block or backup name changing.
  - `hook` in `~/.kiro/hooks/cartographer/<name>/`, registered in the owned
    `~/.kiro/hooks/cartographer.json` (D300). It fires only in
    `kiro-cli chat --v3 --tui`, so the scheduled timer stays advised. The loader
    must stay non-recursive, or the cell has to move out of `~/.kiro/hooks/`.
    The workspace cell stays unsupported.
  - write-findings feedback (D353): **not supportable** (probed on 2.28.0,
    `chat --v3 --tui` with workspace `.kiro/hooks/*.json`, #640). `PostToolUse`
    fires, for `tool_load` as well as for the MCP call, and an MCP tool is named
    `mcp_<server>_<tool>` (single underscores; Claude's matcher never matches);
    `tool_response` is the result text. No output reaches the model: exit 2 with
    stderr, plain stdout, `additionalContext` JSON and `{"decision":"block"}` all
    left the agent unaware.
  - `skill` in `~/.kiro/skills/` (project `.kiro/skills/`), `mcp` in
    `~/.kiro/settings/mcp.json` (a server of the same name in the project
    `.kiro/settings/mcp.json` replaces the global one: the session sees only the project
    server's tools; probe 2026-11-09, Kiro CLI 3.0.0, recipe in `probes.md` §Kiro, MCP shadow probe), `instructions` in `~/.kiro/steering/cartographer.md`.
  - session transcripts, read by the usage scan (D326): `~/.kiro/sessions/**/*.jsonl`.
    Two shapes, tool records only. **V3** (the default engine since 3.0.0, observed on
    3.0.0): `<hash>/sess_<id>/messages.jsonl`, the record's content under `payload`; a
    `payload.type == "tool_call"` with `toolName` `disclose_context` (a skill activation,
    `_meta.kiro.disclosedContext.uri` is the `file://` URL of its `SKILL.md`),
    `read_file` (`args.path`) or `execute_bash` (`args.command`). **V2** (<= 2.x): a
    top-level `kind` object, `kind.BuiltIn.FileRead.operations[].path` or a shell
    command; the Kiro version it was observed on was not recorded. Fixtures (home
    replaced by `$HOME`): `internal/provisioning/testdata/usage/kiro-v3-session.jsonl`
    and `kiro-session.jsonl`.
- **Watch items**:
  - the default `kiro-cli chat` starts firing standalone hooks (its KAS log shows
    `v2 hooks cache initialized`; on 3.0.0 the KAS logs load `cartographer.json` in every V3 session, but
    execution is not proven: probe with a sentinel, then revisit `sessionHookLimit` in
    `internal/provisioning/bootstrap.go:443`, `hookspec.go:29`, `workspacescope.go:155`,
    `docs/interoperability.md:137,158`, `docs/configurator.md:297`). Then drop the `sessionHookLimit` on the Kiro
    hook mechanism, and the timer advice with it;
  - the usage scan's record shape (above) changes: a new Kiro release that renames
    `kind.BuiltIn.FileRead` makes every Kiro skill look unused, silently;
  - Kiro IDE: does it read a global `~/.kiro/hooks`, and fire a `SessionStart`
    from it?
  - Kiro Crew: does `~/.kiro/crew/hooks/` take the same v1 file?

  Probe both on a machine that has them before any matrix cell claims them.
- **Probe notes**: run the real binary
  (`/opt/homebrew/Caskroom/kiro-cli/<v>/kirocli/bin/kiro-cli`, or the app bundle
  `"/Applications/Kiro CLI.app/Contents/MacOS/kiro-cli"`; the Homebrew symlink works now on 3.0.0).
  `kiro-cli agent upgrade --dry-run` works without login. The V3 TUI asks before running an MCP tool, and a
  scripted `\r` does not answer it; use `-a` and accept its warning (down arrow,
  then Enter) before typing the prompt. A first run on a clean profile creates
  `~/.kiro/{logs,sessions,session-index,agents}`: remove them afterwards. Three agent engines coexist (`--agent-engine
  v1|v2|v3`, default `v2`): a feature present in a non-default engine is not
  "supported". An interactive probe needs a pty. Details in the probe recipe.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill unverified
global mcp unverified
global agent.delegate unverified
global agent.invoke unverified
global hook.PostInvocation absent
global hook.PostToolUse absent
global hook.PreCompact absent
global hook.PreInvocation absent
global hook.PreToolUse absent
global hook.SessionStart unverified
global hook.Stop unverified
global hook.SubagentStop absent
global hook.UserPromptSubmit unverified
project skill verified 2026-11-05 kiro-project
project mcp verified 2026-11-05 kiro-project
project agent.delegate verified 2026-11-05 kiro-project
project agent.invoke verified 2026-11-05 kiro-project
project hook.PostInvocation absent
project hook.PostToolUse absent
project hook.PreCompact absent
project hook.PreInvocation absent
project hook.PreToolUse absent
project hook.SessionStart absent
project hook.Stop absent
project hook.SubagentStop absent
project hook.UserPromptSubmit absent
```

## opencode

- **Aligned with**: `2.2.5` — 2026-11-06, MCP tool visibility (#1714): Code Mode is on by default for MCP
  servers; `codemode` is per server (a flat `mcp.<name>` entry keeps it since 2.2.0); the inline catalog is
  2 000 tokens, shortest signatures first, with no MCP pin (anomalyco/opencode#62316); server instructions
  are shown under "use through `execute`". `2.0.30` — 2026-10-17, probed: project agents in `.opencode/agents/`
  (`opencode debug agents` lists them; it does not load `.claude/agents/`) and
  `.agents/skills/` found, `.opencode/skills/` winning. `2.0.25` — 2026-10-09, probed (#478, #638): global skill and
  agent discovery, agent `permission`, the write-findings channel. `1.18.34` —
  2026-10-01 (documentary review). Earlier probe: `1.18.20`, date `unknown` (D192).
- **Sources**: repository <https://github.com/anomalyco/opencode> (`sst/opencode`
  redirects there), releases <https://github.com/anomalyco/opencode/releases>,
  changelog <https://opencode.ai/changelog>; version: npm `opencode-ai`; docs
  <https://opencode.ai/docs/agents>, `/skills`, `/plugins`, `/mcp-servers`,
  `/rules`, `/config`.
- **Depends on**:
  - MCP tool hiding (D1724; read 2026-11-06, OpenCode 2.2.5): Code Mode wraps every MCP server, with an inline catalog of 2000 tokens filled shortest signature first, and no server-side pin for MCP (upstream anomalyco/opencode#62316). Cartographer does not write `codemode: false`. The entry tools keep short input schemas (a test caps them) so they stay in the inline catalog, and the entry paragraph of `initialize.instructions` names them (OpenCode prefixes its own "use through `execute`" line). Read in the installed 2.2.5 binary: its MCP client reads no per-tool `_meta` for exposure (the only `_meta` consumer found is the server-info key; Code Mode is switched per server by `codemode`), consistent with the missing pin. Not yet verified in a real 2.2.x session (#1714).
  - Stdio bridge (D737): spawned in the session's folder but with `$PWD` set to `$HOME` (probe 2026-10-17, OpenCode 2.0.30), which is why the bridge reads `os.Getwd`, never `$PWD`; `roots` declared with `[cwd]`.
  - Writer header (D904): an HTTP entry carries it in `headers`; the token's `${VAR}` becomes `{env:VAR}`, the author is a literal.
  - `agent` global `~/.config/opencode/agents/` (the `config` directory of
    `opencode debug paths`, probed on 2.0.25 and the documented 1.x path, D360,
    guarded by `clientcompat_test.go`; `~/.opencode/agent/` is not loaded on 2.x):
    Markdown with `description` + `mode: subagent`. Native restriction key
    `permission` (`edit`, `bash`, … allow/ask/deny) is documented (D291); `tools`
    is no longer shown in the agents doc: use `permission` instead, `tools` may
    still work but is undocumented (D320).
  - `skill` global `~/.config/opencode/skills/` (probed on 2.0.25, D360); project
    `.opencode/skills/` (documented, not probed on 2.x). Hooks fire through a
    generated JS plugin in `~/.config/opencode/plugins/` (documented, autoloaded) and, in a workspace,
    `<workspace>/.opencode/plugins/` (probed on 2.2.5, 2026-11-06; 1.x documented, not probed; the user path is not
    read from a project; D1841)
    (D59; shape per installed major, 1.x and 2.x, D359: 2.x rejects the 1.x shape; an unreadable version writes the newest shape with a warning, D719; both shapes bind the command to a JS `const` and run ``sh -c ${command}``, so a backtick or `${VAR}` in it reaches the shell untouched;
    sources `opencode.ai/v2/docs/build/plugins/` and `/migrate-v1`), files kept in `~/.opencode/hooks/` (never discovered by the client, run by path); sync layer 1 is the documented
    `session.created` event. A hook command whose executable has a character outside `[A-Za-z0-9_./:@%+=,-]` is single-quoted on a POSIX host in every registrar (`'\''` for an apostrophe); Kiro on Windows keeps the host's spelling, its command shell (PowerShell or cmd.exe) not being verified.
  - `mcp` under the `mcp` key of `~/.config/opencode/opencode.json`, the global
    config file of both majors (D1172: `$HOME/opencode.json` is only a project
    file found by walking up from the session folder, and 1.x stops at the git
    root); `instructions` in `~/.config/opencode/AGENTS.md` (both documented).
- **Probed on 2.0.25** (2026-10-09):
  - **the former global `skill` and `agent` cells were not loaded** (moved by
    D360): a skill in `~/.opencode/skills/` is missing from the skill tool ("Unable to load skill"),
    an agent in `~/.opencode/agent/` is missing from `opencode debug agents`;
    the same files in `~/.config/opencode/{skills,agents}/` load. KB skills reach
    OpenCode only through its `~/.claude/skills` scan.
  - agent `permission: {edit: deny, bash: deny}` resolves to `edit`/`shell` deny
    rules in `opencode debug agents`; the behaviour was not verifiable (the free
    tier refused to run that agent).
  - write-findings (D353, #638): `ctx.tool.hook("execute.after")` fires for the
    code-mode `execute` tool and for the inner MCP call, named `<server>_<tool>`
    with the result text in `result.output`. Appending to `event.result.output`
    reaches the model and keeps the result; throwing reaches it as a tool error
    and the agent retries the write. The generic plugin passes no payload on
    stdin. Implemented as a dedicated plugin for 2.x only (D362): it appends the
    message to `result.output` on exit 2 and never throws; 1.x was not probed and
    gets nothing. The inner call's output joins the write response and the
    server's sync-state block with a newline; the subcommand reads both values.
  - `opencode --version` prints `opencode v2.0.25` (seen on both streams); the major parser reads it (D719). A sync run without `opencode` on `PATH` (a timer's) reads the version as unknown: it keeps the installed write-findings plugin with a warning and removes it only on a version read as < 2 (#1715). `session.created` delivery to `ctx.event.subscribe` is not yet probed.
  - 2.x `opencode.json` is translated on load (`mcp` → `mcp.servers`,
    `permission` map → `permissions` list), so the 1.x shape still works.
- **Watch items**: the project skill cell (`.opencode/skills`) loads on 2.2.5
  (probe 2026-11-05). On 2.2.5 project agents load from both `.opencode/agent/` (the
  matrix cell) and `.opencode/agents/`, so the two no longer disagree. `session.idle`
  never reached a plugin in `opencode run`: `hook.Stop` stays unverified, and the
  global MCP cell was not probed (`opencode mcp list` failed against the background
  service of the real install).
- **Probe notes**: 2.x has no `opencode agent list`: use `opencode debug agents`
  (JSON) and `opencode debug paths`. A background service caches the
  configuration: run `opencode reload` after changing a file. `opencode run -m
  opencode/big-pickle` works without credentials, but the free tier refuses some
  custom primary agents ("can only be used from within OpenCode").
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill verified 2026-11-05 opencode-scratch-xdg
global mcp unverified
global agent.delegate verified 2026-11-05 opencode-scratch-xdg
global agent.invoke verified 2026-11-05 opencode-scratch-xdg
global hook.PostInvocation absent
global hook.PostToolUse verified 2026-11-05 opencode-scratch-xdg
global hook.PreCompact absent
global hook.PreInvocation absent
global hook.PreToolUse verified 2026-11-05 opencode-scratch-xdg
global hook.SessionStart verified 2026-11-05 opencode-scratch-xdg
global hook.Stop unverified
global hook.SubagentStop absent
global hook.UserPromptSubmit absent
project skill verified 2026-11-05 opencode-scratch-xdg
project mcp verified 2026-11-05 opencode-scratch-xdg
project agent.delegate verified 2026-11-05 opencode-scratch-xdg
project agent.invoke verified 2026-11-05 opencode-scratch-xdg
project hook.PostInvocation absent
project hook.PostToolUse verified 2026-11-05 opencode-scratch-xdg
project hook.PreCompact absent
project hook.PreInvocation absent
project hook.PreToolUse verified 2026-11-05 opencode-scratch-xdg
project hook.SessionStart verified 2026-11-05 opencode-scratch-xdg
project hook.Stop unverified
project hook.SubagentStop absent
project hook.UserPromptSubmit absent
```

## antigravity

- **Aligned with**: CLI `agy` `1.4.0` — 2026-11-06, MCP tool visibility: per-tool
  `_meta["antigravity/alwaysLoad"]` (since 1.3.3); `enabledTools`/`disabledTools` in `mcp_config.json`.
  `1.3.2` — 2026-10-09, probed (#478, #639). App
  `2.19.1` — 2026-10-01 (documentary review; the app was not available for the
  probe). The CLI is versioned separately (`agy --version`).
- **Sources**: changelog <https://antigravity.google/docs/changelog/>; CLI
  changelog
  <https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md>;
  docs <https://antigravity.google/docs/rules> (`/docs/rules-workflows/` redirects
  there), `/docs/skills`, `/docs/subagents`, `/docs/hooks`, `/docs/mcp`; installed
  version source: `unverified`.
- **Depends on** (what the matrix encodes; D193/D207, not re-probed):
  - MCP tool hiding (D1724; read 2026-11-06, `agy` 1.4.0): tools are discovered on demand; `_meta["antigravity/alwaysLoad"]: true` per tool (since 1.3.3) keeps the entry tools declared. Whether `instructions` are shown is unknown.
  - Stdio bridge (D737): not probed (2026-10-17) for the IDE nor for `agy`. If the host spawns the server outside the workspace and declares no `roots`, the session gets the provider's default set; `cartographer workspace resolve` shows what the bridge would pick for a folder.
  - Writer header (D904): an HTTP entry carries it in `headers`.
  - Global, under `~/.gemini`: `skill` `config/skills`, `agent`
    `config/agents` (Markdown: `name`, `description`, `mainAgent: false`,
    `subagent: true`), `hook` `config/hooks` registered in
    `~/.gemini/config/hooks.json`, `mcp` `config/mcp_config.json`, `instructions`
    `GEMINI.md`.
  - Project-local (D364): `skill` `.agents/skills`, `agent` `.agents/agents`
    (same translation), `hook` `.agents/hooks/<n>/` registered in
    `.agents/hooks.json` (the file is picked from the hook's path), `mcp`
    `.agents/mcp_config.json`, `instructions` the `AGENTS.md` block. Rules
    (`.agents/rules/`) are deliberately unused: they need `trigger: always_on`.
  - Native hooks support `SessionStart` (D1007), `PreToolUse`, `PostToolUse`,
    `PreInvocation`, `PostInvocation`, and `Stop`. Sync layer 1 installs the
    `cartographer-bootstrap` hook for Antigravity (D60, D1007). Agent frontmatter
    documents `tools`, `model` and `commandExecutionPolicy` (candidate D291 keys,
    not verified).
- **Probed on CLI 1.3.2** (2026-10-09):
  - workspace scope confirmed (now the project cells above): `.agents/skills/`, `.agents/agents/`,
    `.agents/hooks.json`, `.agents/mcp_config.json` (used in the session even
    though `agy mcp list` shows only global servers), `AGENTS.md`, and
    `.agents/rules/*.md` only with frontmatter `trigger: always_on`; nothing in
    a directory without `.agents/`.
  - globals: the CLI reads `~/.gemini/config/{skills,agents}` (the current
    cells), not `~/.gemini/antigravity-cli/{skills,agents}` as its docs say.
  - agent `tools: [view_file]` + `commandExecutionPolicy: never` did not
    restrict the agent (it ran a shell command): not D291 keys.
  - write-findings (D353, #639): **not supportable**. `PostToolUse` fires for an
    MCP call as tool `call_mcp_tool` (args `ServerName`, `ToolName`,
    `Arguments`), so Claude's matcher never matches, and the payload
    (`toolCall`, `error`, `conversationId`, `stepIdx`, `transcriptPath`,
    `workspacePaths`, …) **has no tool result**. A failed hook (exit 2, stderr)
    does reach the model; exit 0 with stderr does not.
- **Watch items**: the app's workspace scope and `.gemini/config.json` (2.17.0),
  not probed; a `SessionStart` event would change the layer-1 trigger; a tool
  result in the `PostToolUse` payload would reopen write-findings.
- **Probe notes**: `agy -p "<prompt>" --print-timeout 240s
  --dangerously-skip-permissions </dev/null`; the MCP test server must be global
  (`~/.gemini/config/mcp_config.json`) or in `.agents/mcp_config.json`. Back up
  and restore `~/.gemini/config/*` around a global probe.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill unverified
global mcp unverified
global agent.delegate unverified
global agent.invoke unverified
global hook.PostInvocation unverified
global hook.PostToolUse unverified
global hook.PreCompact absent
global hook.PreInvocation unverified
global hook.PreToolUse unverified
global hook.SessionStart unverified
global hook.Stop unverified
global hook.SubagentStop absent
global hook.UserPromptSubmit absent
project skill unverified
project mcp unverified
project agent.delegate unverified
project agent.invoke unverified
project hook.PostInvocation unverified
project hook.PostToolUse unverified
project hook.PreCompact absent
project hook.PreInvocation unverified
project hook.PreToolUse unverified
project hook.SessionStart unverified
project hook.Stop unverified
project hook.SubagentStop absent
project hook.UserPromptSubmit absent
```

## crush

- **Aligned with**: `v0.100.0` — 2026-11-06, MCP tool visibility: all MCP tools are sent; server
  instructions are appended in `<mcp-instructions>`. v0.99.0 added lifecycle hook events (session, turn,
  permission, compaction, `PostToolUseFailure`) and v0.100.0 background subagents: check them against the
  crush hook cell (D363) in a follow-up probe. `v0.98.0` — 2026-10-09, probed (#478).
- **Sources**: releases <https://github.com/charmbracelet/crush/releases>; README
  <https://github.com/charmbracelet/crush>; config
  <https://github.com/charmbracelet/crush/tree/main/docs/config>; hooks
  <https://github.com/charmbracelet/crush/tree/main/docs/hooks>; JSON schema
  <https://charm.land/crush.json>; package: Homebrew tap
  `charmbracelet/tap/crush`, npm `@charmland/crush` (not in homebrew-core).
- **Depends on**:
  - Stdio bridge (D737): spawned in the session's folder (probe 2026-10-17, Crush 0.98.1); `roots` declared with an empty list, so the working directory decides.
  - Writer header (D904): an HTTP entry carries it in `headers`; a value Crush would expand (`$VAR`, `$(cmd)`) is written in the encoded form, never as is. `${VAR}` becomes `$VAR` (the documented form) unless a name character follows (`${PREFIX}_v2` stays braced, since `$PREFIX_v2` is another variable); a literal `$` in a value is left as written, because how Crush escapes one is not documented.
  - `mcp` in `~/.config/crush/crush.json` — JSON Crush documents as deprecated (no
    new options) in favour of `crushrc`, a Bash file Cartographer refuses to
    write; `$VAR` form for values, `$(` refused (D225). JSON is still read and
    merged with `crushrc`.
  - `instructions` in `~/.config/crush/CRUSH.md` (Crush also loads
    `~/.config/AGENTS.md`); `skill` in `~/.config/crush/skills/` (project
    `.crush/skills/`; Crush also scans `~/.claude/skills`, `~/.agents/skills` and
    the `.agents`/`.claude`/`.cursor` project skill directories).
  - `hook` in `~/.config/crush/hooks/<name>/`, registered as `hooks.PreToolUse`
    entry `cartographer-<name>` in `crush.json` (D363); project: `.crush.json` and
    `.crush/hooks/`. `mcp` also has a project cell, `.crush.json`.
  - `agent` unsupported (no documented subagent mechanism); sync layer 1 is the
    timer.
- **Probed on v0.98.0** (2026-10-09):
  - hooks: `hooks.PreToolUse` in `~/.config/crush/crush.json` and in a project
    `.crush.json` fire (negative control: none); of six events tried only
    `PreToolUse` fires. Exit 2 blocks the tool and its stderr reaches the model.
    Payload: `cwd`, `event`, `session_id`, `tool_input`, `tool_name` (lowercase,
    e.g. `bash`). There is no session event (D363).
  - a project `.crush.json` `mcp` entry is used by the session (D363: project
    `mcp` and `hook` cells; `agent` and `instructions` not probed).
  - the global `crush.json` is still read when a `crushrc` exists.
- **Watch items**: `crush.json` becoming unreadable, or `crushrc` the only
  format, would remove the `mcp` cell; a session event would allow the bootstrap
  hook, a `PostToolUse` that fires the write-findings hook.
- **Probe notes**: `crush run -q -m openai/gpt-5.5 "<prompt>" </dev/null` (the
  default model may be refused by a ChatGPT account); `--yolo` is a root flag,
  not accepted by `run`. `~/.config/crush/` may not exist: create it for a global
  probe and remove it afterwards.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill unverified
global mcp unverified
global agent.delegate absent
global agent.invoke absent
global hook.PostInvocation absent
global hook.PostToolUse absent
global hook.PreCompact absent
global hook.PreInvocation absent
global hook.PreToolUse unverified
global hook.SessionStart absent
global hook.Stop absent
global hook.SubagentStop absent
global hook.UserPromptSubmit absent
project skill unverified
project mcp unverified
project agent.delegate absent
project agent.invoke absent
project hook.PostInvocation absent
project hook.PostToolUse absent
project hook.PreCompact absent
project hook.PreInvocation absent
project hook.PreToolUse unverified
project hook.SessionStart absent
project hook.Stop absent
project hook.SubagentStop absent
project hook.UserPromptSubmit absent
```

## copilot

- **Aligned with**: `1.0.98` — 2026-10-17, probed (latest seen `1.0.106`, 2026-11-06, not re-probed: tool
  search once the tool set is large, `deferTools: "never"` per server, `toolSearch: false` to disable; re-align after a
  probe on 1.0.106): project agents in
  `.github/agents/<name>.agent.md` load once the folder is trusted, the `model` key is
  honoured and an unavailable model falls back to `auto` with a warning.
  `1.0.94` — 2026-10-10, probed (#676). The GitHub Copilot CLI
  (`copilot`), not Copilot in VS Code.
- **Sources** (`unverified`): releases <https://github.com/github/copilot-cli/releases>;
  docs <https://docs.github.com/copilot/how-tos/use-copilot-agents/use-copilot-cli>.
  Every cell below comes from a probe with `COPILOT_HOME` pointed at a scratch
  directory, not from these pages.
- **Depends on**:
  - MCP tool hiding (D1724; read 2026-11-06, Copilot CLI 1.0.106): tool search above a size threshold; no per-tool pin (`deferTools: "never"` is a whole-server opt-out Cartographer does not write). Only the entry paragraph of `initialize.instructions` helps; whether it is shown is not established.
  - Stdio bridge (D737): spawned in the session's folder (probe 2026-10-17, Copilot CLI 1.0.98); `roots` not declared.
  - Writer header (D904): an HTTP entry carries it in `headers`, next to `Authorization`.
  - `mcp` in `~/.copilot/mcp-config.json` (`mcpServers`, `type: "http"`, `headers`,
    `"tools": ["*"]`); `${VAR}` in a header value is expanded from the environment.
    Without `tools` a non-interactive session is offered nothing from the server.
  - `instructions` in `~/.copilot/copilot-instructions.md` ("Personal instructions"
    in `copilot instruction list`); project `AGENTS.md` ("Repository instructions").
  - `skill` in `~/.copilot/skills/<name>/` ("Personal skills"); project
    `.agents/skills/<name>/` ("Project skills"; `.github/skills/` is listed too).
  - `agent` in `~/.copilot/agents/<name>.agent.md` (frontmatter `name`,
    `description`); a session reported seeing it.
  - `hook` in a dedicated `~/.copilot/hooks/cartographer-<name>.json`
    (`{"version":1,"hooks":{"<event>":[{"type":"command","bash":…,"powershell":…}]}}`);
    `~/.copilot/settings.json` also takes inline hooks and is never patched (D676).
    The KB hook's own files go to `~/.copilot/cartographer-hooks/<name>/`, outside
    `hooks/`: Copilot loads `~/.copilot/hooks/` recursively and parses every
    `*.json` below it (probed: a non-Copilot JSON in a subdirectory logged
    "Invalid hook configuration … hooks must be an object").
  - Project `mcp`, `agent` and `hook` cells unsupported: `.mcp.json` /
    `.github/mcp.json` are documented but not listed by `copilot mcp list` in an
    untrusted folder, and `.github/agents` and `.github/hooks` are gated on folder
    trust (`--help`).
  - `COPILOT_HOME` is not honoured: paths are `$HOME/.copilot`, like every provider.
- **Probed on 1.0.94** (2026-10-10):
  - hooks: `sessionStart`, `preToolUse`, `postToolUse` fire (other events not
    probed, not claimed). Payload (camelCase): `sessionId`, `timestamp`, `cwd`,
    `toolName`, `toolArgs`; `postToolUse` adds `toolResult: {resultType,
    textResultForLlm}`. No matcher field: a hook fires for every tool.
  - `preToolUse` stdout `{"permissionDecision":"deny","permissionDecisionReason":…}`
    blocks the call; `postToolUse` stdout `{"additionalContext":…}` (top level) is
    appended to the tool result the model reads (the write-findings hook, D676).
  - MCP tools are `<server_name>-<tool>` in hook payloads. `textResultForLlm` is the
    response JSON followed, with no separator, by a second object
    (`{"last_error":"","sync_state":"pending"}`).
  - session transcripts: `~/.copilot/session-state/<session>/events.jsonl`; a skill
    activation is `tool.execution_start` with `data.toolName: "skill"` and
    `data.arguments.skill`.
  - non-interactive: `copilot -p "<prompt>" -s`; tools need a grant
    (`--allow-tool <server_name>`, `--allow-all-tools`), passed with `--client-flag`.
- **Watch items**: project cells under folder trust (they would flip the three
  unsupported project cells); whether `sessionStart` output reaches the model (the
  bootstrap hook only needs its side effect); a matcher field.
- **Probe notes**: `COPILOT_HOME=<scratch> copilot instruction list | skill list |
  mcp list` show the cells without a model; hooks need `copilot -p … -s
  --allow-all-tools`. Never point the probe at `$HOME/.copilot`.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill unverified
global mcp unverified
global agent.delegate unverified
global agent.invoke unverified
global hook.PostInvocation absent
global hook.PostToolUse unverified
global hook.PreCompact absent
global hook.PreInvocation absent
global hook.PreToolUse unverified
global hook.SessionStart unverified
global hook.Stop absent
global hook.SubagentStop absent
global hook.UserPromptSubmit absent
project skill unverified
project mcp absent
project agent.delegate absent
project agent.invoke absent
project hook.PostInvocation absent
project hook.PostToolUse absent
project hook.PreCompact absent
project hook.PreInvocation absent
project hook.PreToolUse absent
project hook.SessionStart absent
project hook.Stop absent
project hook.SubagentStop absent
project hook.UserPromptSubmit absent
```

## pi

- **Aligned with**: `1.5.0` — 2026-11-03, probed; unchanged on 2026-11-06 (MCP tool visibility: modes
  `codemode|deferred|direct|hidden`, per-tool `toolExposure`, `codemode.inlineBudget` 3 000, feeding #1714
  item 4). Pi (`pi`), npm
  `@earendil-works/pi-coding-agent`.
- **Sources**: repository <https://github.com/earendil-works/pi>; docs
  `packages/coding-agent/docs/{configuration,mcp,extensions,skills,cli}.md` of the installed
  package; npm `@earendil-works/pi-coding-agent`.
- **Depends on**:
  - Stdio bridge (D737): spawned in the session's folder (probe 1 below). The token variable
    reaches the bridge: Pi does not filter the environment of an MCP server (probed with a stub
    server that recorded a variable exported to `pi mcp list`), so no `PassEnv` is emitted.
  - `mcp` in `~/.pi/agent/mcp.json` (`mcpServers`). Cartographer's own entry keeps Pi's default
    exposure, `codemode`, and pins the entry tools with `"toolExposure": {"<tool>": "direct"}`
    (D1724, which amends D1603): the entry tools are declared to the model, the
    others are reachable from codemode (`inlineBudget` 3000 tokens, shortest first). A third-party
    server of a KB `mcp` artifact keeps `"exposure": "direct"`. The `toolExposure` shape is from
    Pi's `mcp.md` (1.5.0). The keys are the server's bare tool names, not the `mcp__<server>__<tool>`
    form the model sees: read in the installed 1.5.0 package, `getMcpToolExposure(config, tool.name)`
    (`dist/core/mcp-servers.js`) looks `tool.name` up in `toolExposure` (exact name first, then `*`
    patterns), and the extension calls it with the name the server listed (`dist/extensions/mcp/index.js`,
    `cli.js`); the prefixed name is only built afterwards for the declared tool. Pi has no `_meta` or
    `alwaysLoad` handling for MCP tools (grep of `dist/extensions/mcp`). A session probe that the entry
    tools are declared and the others reachable from codemode is not yet observed (#1714). `${VAR}` in `env` and
    `headers` is Pi's own interpolation, written verbatim; a value that is wholly `!command` runs
    at load and is refused. A server name outside letters, digits, `_` and `-` is skipped by Pi
    and refused by the emitter.
  - `instructions` in `~/.pi/agent/AGENTS.md`; an `AGENTS.override.md` replaces `AGENTS.md` or
    `CLAUDE.md` only in the same directory (the package's configuration page), which is the declared
    `InstructionsPrecedence`. Project: `AGENTS.md` (probe 2).
  - `skill` in `~/.pi/agent/skills/<name>/`.
  - `agent` unsupported (Pi has no subagents); no project `.pi/*` cell (trust-gated).
  - `hook`: one generated extension per hook, `~/.pi/agent/extensions/cartographer-<name>.ts`
    (loaded without trust, probe 4), the hook's own files in `~/.pi/agent/cartographer-hooks/<name>/`
    (Pi also autoloads `extensions/*/index.ts`). Events `session_start` (every reason), `tool_call`,
    `tool_result`; payload fields `toolName`, `input`, `toolCallId`, `content`, `isError`; exit 2
    blocks (`{block, reason}`) or appends a text block to `content` (`structuredContent` returned
    too); stdout ignored. `Stop`/`UserPromptSubmit` are not mapped (not probed).
  - `PI_CODING_AGENT_DIR` is not honoured: paths are `$HOME/.pi/agent`, like every provider
    (D676, D755); symlink `~/.pi/agent` to use another directory. Detection does not read it either.
  - Detection: `pi` on `PATH` counts only when `pi --version` prints a bare version; other tools
    ship a `pi` (a shim forwarding to the real binary passes, an unrelated one does not).
- **Probed on 1.5.0** (2026-11-03):
  1. A global `<agent-dir>/mcp.json` entry `{"command":"<cartographer>","args":["mcp","--provider","<id>"],"exposure":"direct"}`:
     `pi mcp list` shows it connected with its tools (direct, global). In a session the tools are
     `mcp__cartographer__<tool>` (the Claude Code form), a `search` call worked, and the bridge
     resolved the session folder's workspace binding (a KB bound only to that folder was
     reachable): the bridge is spawned in the session cwd.
  2. Instructions, untrusted, from `W/repo/` (git repository inside non-git `W`): read global
     `<agent-dir>/AGENTS.md`, `W/AGENTS.md` (above the git root: the ancestor walk does not stop
     at the root) and `W/repo/CLAUDE.md`; `W/repo/CLAUDE.local.md` NOT read.
  3. Skills, untrusted (`-na`): only global `<agent-dir>/skills/`; project `.agents/skills/` NOT
     loaded. Trusted (`-a`) from `W/repo/sub`: `W/repo/.agents/skills/probe-dup` loaded and WON
     over a same-named global `<agent-dir>/skills/probe-dup` (no warning in `-p` output);
     `W/.agents/skills/` above the root NOT loaded.
  4. A global extension `<agent-dir>/extensions/*.ts` loads without trust; `session_start` fires
     with `reason: "startup"` and `ctx.cwd` = the session folder; `before_agent_start` fires;
     `tool_call` fires and returning `{block:true, reason}` blocked a `bash` call with the reason
     reaching the model verbatim; `tool_result` fires after an MCP call.
- **Watch items**: the `tool_result` return contract (a partial patch; replacing `content` drops
  `structuredContent`) comes from the vendor docs and is not yet exercised end to end on a real Pi
  (the write-findings text reaching the model, the `PreToolUse` block repeated by the model, the
  bootstrap hook in `pi -p`): probe it before relying on it; `Stop`/`UserPromptSubmit` unmapped;
  `SessionStart` stdout (the update notice) is not delivered; jiti and the extension API may change;
  `/reload` re-fires `session_start`, so the sync runs again (harmless); a trusted project `.agents/skills/<x>` shadows the global copy (Cartographer
  also projects `.agents/skills` for codex, copilot and antigravity: same content, benign
  duplication, not suppressed); the `CLAUDE.md` global fallback order is unprobed; the Windows
  path `%USERPROFILE%\.pi\agent` is unverified; `pi` shims on `PATH`; an exposure changed by the
  user in `/mcp` is rewritten by the next sync.
- **Probe notes**: `pi -p --no-session --model <provider/id> [-a|-na] "<prompt>"`, `pi mcp list`;
  a scratch `PI_CODING_AGENT_DIR` for probes only (copy `auth.json` when a model is needed),
  never `$HOME/.pi/agent`.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill verified 2026-11-05 pi-scratch-agent-dir
global mcp verified 2026-11-05 pi-scratch-agent-dir
global agent.delegate absent
global agent.invoke absent
global hook.PostInvocation absent
global hook.PostToolUse verified 2026-11-05 pi-scratch-agent-dir
global hook.PreCompact absent
global hook.PreInvocation absent
global hook.PreToolUse verified 2026-11-05 pi-scratch-agent-dir
global hook.SessionStart verified 2026-11-05 pi-scratch-agent-dir
global hook.Stop absent
global hook.SubagentStop absent
global hook.UserPromptSubmit absent
project skill absent
project mcp absent
project agent.delegate absent
project agent.invoke absent
project hook.PostInvocation absent
project hook.PostToolUse absent
project hook.PreCompact absent
project hook.PreInvocation absent
project hook.PreToolUse absent
project hook.SessionStart absent
project hook.Stop absent
project hook.SubagentStop absent
project hook.UserPromptSubmit absent
```

## cline

- **Aligned with**: `4.1.26` — 2026-11-04, unchanged on 2026-11-06 (every MCP tool is sent; no deferral), read from the bundle (`dist/extension.js` of the VS Code
  extension `saoudrizwan.claude-dev`); nothing was exercised on a live Cline except where a note says so.
- **Sources**: `unverified` vendor documentation; the cells come from the extension bundle and the npm
  package `cline` (3.0.74, `bin = { cline: 'bin/cline' }`).
- **Depends on**:
  - Stdio bridge (D737). Cline declares no MCP `roots` capability and starts a stdio server with the
    extension host's working directory (`/` on macOS) unless the entry sets `cwd`, so the bridge resolves an
    unbound folder and serves the provider default. Cartographer never writes `cwd`, `disabled`,
    `autoApprove` or `timeout`.
  - `mcp` in `~/.cline/data/settings/cline_mcp_settings.json` (`mcpServers`), hot-reloaded; entry
    `{type: "stdio", command, args, env}` or `{type: "streamableHttp", url, headers}` (`sse` exists in Cline, no
    KB descriptor carries it). The file is Cline's own (a `.bak` beside it): other servers and keys survive and
    it is never deleted. `${VAR}` expansion in `headers`/`env` was **not** verified, so `SupportsMCPHeaders` is
    false and a KB `mcp` artifact with headers is materialised without them, with the warning; a stdio `env`
    value is written verbatim and unprobed. Cartographer's own entry is the bridge and carries no header.
  - Tool names: the model sees `<server>__<tool>` (bundle `pCn`), not a flat namespace; `mcp__<server>__<tool>`
    is the permission-rule syntax.
  - `instructions` in `~/.cline/rules/cartographer.md`, a file Cartographer owns whole. The global rules list is
    `$CLINE_DIR/rules`, `~/Cline/Rules`, `Documents/Cline/Rules` and the OneDrive variant, all through **one**
    loader: `.md`, `.markdown` or `.txt`, optional `name` and `disabled`/`enabled` frontmatter only, non-empty
    body; no path-conditional frontmatter, so a plain file is an always-on global rule (the Antigravity
    `always_on` trap, D364, does not apply, but it was not confirmed on a live session).
  - `skill` in `~/.cline/skills/<name>/`; `~/.claude/skills` is read only at project level.
  - `agent` in `~/.cline/agents/<name>.yaml`: only `.yml`/`.yaml` load; frontmatter `name` and `description`
    (required), `tools`, `skills`, `providerId`, `modelId`, `maxIterations`; the body is the system prompt and an
    empty one skips the file silently. Exposed to the model as `subagent_<name>`, capped at 64 characters.
  - `hook` in `~/.cline/cartographer-hooks/<name>/` (the hook's own files) plus one Cartographer-owned dispatcher per
    needed event in `Documents/Cline/Hooks` (`TaskStart` for `SessionStart`, `UserPromptSubmit`, `PreToolUse`,
    `PostToolUse`; `Stop`, `SubagentStop` and `PreCompact` are not claimed: nothing fires them in a way that could
    continue the run). Every project cell is unsupported (no roots).
  - Hooks, read from the bundle (4.1.26; none of it run live). It carries two loaders. The one the VS Code
    extension runs reads the global `Documents/Cline/Hooks` and `<workspace>/.clinerules/hooks` only
    (`$HOME/.cline/hooks` and `<workspace>/.cline/hooks` belong to the SDK loader, which the VS Code host switches
    off; the CLI is inferred to run it too and reads `Documents/Cline/Hooks` as well). `Documents` is
    `[Environment]::GetFolderPath('MyDocuments')` on Windows (a redirected folder is honoured), `xdg-user-dir
    DOCUMENTS` on Linux, `$HOME/Documents` on macOS. Every file found runs, in parallel, none shadows another;
    the only collision is two writers of one path. The file is `<Event>` with no extension and the exec bit on
    POSIX (run as `sh -c`, so a shebang is needed; the exec bit is the per-hook toggle of the Cline UI, `chmod 644`
    disables it), `<Event>.ps1` on Windows (run as `powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass
    -File`; no `.cmd`, no toggle). Switch: the global `hooksEnabled` (default on). Timeout 30 s, then SIGTERM; a
    failed hook never breaks the run. Events fired: `TaskStart` and `UserPromptSubmit` (before the run),
    `PreToolUse`, `PostToolUse`, `TaskComplete`, `TaskCancel`; `TaskResume`, `TaskError`, `PreCompact`,
    `Notification` and `SessionShutdown` exist and are never created. Stdin is JSON: `hookName`, `taskId`,
    `workspaceRoots`, `model`, plus `preToolUse{toolName, parameters}`, `postToolUse{toolName, parameters, result,
    success}`, `userPromptSubmit{prompt}`; every parameter is a string and `result` is `String(output)`. Stdout is
    read as one JSON object with `cancel`, `contextModification` and `errorMessage` only (`review`, `context` and
    `overrideInput` are read by the SDK loader alone); **`cancel` stops the whole task**, there is no per-call skip.
    **Nothing in the payload or the environment names the invoking client**, so a second build that reads
    `Documents/Cline/Hooks` runs the very same files.
  - `cartographer hook dispatch --provider cline --event <Event>` (internal) is the logic the dispatcher files
    call: it reads Cline's payload and runs the Cartographer hooks of the event, materialized under
    `~/.cline/cartographer-hooks/<name>/` (a Claude-style `hook.json` and command, run as `sh -c`, payload on stdin,
    25 s in total, kill grace included; each stream capped at 1 MiB). `TaskStart` runs the `SessionStart` hooks (once per task: a 12 h marker in a private per-user directory of the temp dir),
    `UserPromptSubmit`, `PreToolUse` and `PostToolUse` run their own. `PreToolUse` exit 2 returns `cancel`, which
    stops the task; `PostToolUse` exit 2 with stderr is context, never `cancel`, and only a successful tool fires it;
    `SessionStart` and `UserPromptSubmit` stdout is context; anything else has no effect. A tool `<server>__<tool>`
    is presented as `mcp__<server>__<tool>` and the matcher (unanchored, case-insensitive, honoured on `PreToolUse` and `PostToolUse` only) is tried on both names; a
    Cline built-in (`run_commands`, `editor`) is not translated to Claude's `Bash`. Output is one line of ASCII
    JSON, exit 0 in every case. The write-findings hook is not installed for Cline until probe P2 shows what
    `postToolUse.result` is for an MCP response.
  - The dispatcher (`internal/provisioning/clinehooks.go`, D1663) is a three-line shim: POSIX `<Event>` (`#!/bin/sh`, mode
    0755) or Windows `<Event>.ps1` (UTF-8 with BOM, ASCII otherwise: Windows PowerShell 5.1 reads BOM-less UTF-8 as ANSI),
    both fail-open (a missing binary prints `{}` and exits 0) and exiting 0 whatever `cartographer` returned; the
    payload reaches it by stdin inheritance. Ownership is the token `cartographer:cline-hook-dispatcher <Event>` in the
    first 512 bytes, never a lock entry (a foreign `TaskStart` fails the bootstrap hook with the warning). A file at the path without it (the user's own hook) is never read further,
    rewritten or merged: the hooks are installed, not wired, and the sync warns. An owned file is rewritten with its
    mode kept (a user's `chmod 644` stays), and removed, only while it carries the marker, when no hook of its event
    is left on disk. The set of dispatchers is derived from the hooks on disk, so the bootstrap, write-findings and KB
    hooks are one code path in any order. `Documents` is the Windows known folder when the base dir is the real home
    (a OneDrive-redirected folder included), `<home>/Documents` otherwise, and is the root of the symlink walk (a linked Documents folder works, a link at `Cline` or `Cline/Hooks` is refused, and a link at the dispatcher path is reported as a `safewrite` refusal); `xdg-user-dir` is not replicated, so a
    relocated Linux folder leaves the hooks silently unwired. A dispatcher is machine-wide and client-agnostic: any
    other build reading that folder runs it too.
  - Probes, **not run** (the dispatcher is built for either answer): **P1** whether `TaskStart` fires once per task
    or per turn (the 12 h dedupe covers both); **P2** the `toolName` and `result` of a `concept_write` response;
    **P3** that a `PreToolUse` `{"cancel":true,"errorMessage":"x"}` stops the task and shows the message.
  - `CLINE_DIR`, `CLINE_MCP_SETTINGS_PATH` and a relocated Documents folder are not honoured: every destination is
    home-relative (D676, D755).
  - Detection: the `cline` binary, `~/.cline`, or an extension directory `saoudrizwan.claude-dev-*` under the
    `extensions` folder of VS Code, Insiders, Cursor, Windsurf, VSCodium or VS Code Server. A directory left by an
    uninstalled version is a known false positive.
- **Watch items**: MCP `roots` or a per-workspace MCP file (flips the project cells); `${VAR}` expansion in
  `headers` (flips `SupportsMCPHeaders`); a path-conditional rule frontmatter in the rules loader; a rename of the
  extension id (detection); whether the `cline` CLI shares `$HOME/.cline` (inferred, not run); other builds of Cline
  that read different directories; a per-call skip in the extension's hooks; an environment variable or payload field
  naming the client; the SDK hook loader enabled in the VS Code host; a rename of the event files; probes P1-P3 above.
- **Probe notes**: a scratch `Documents/Cline/Hooks/<Event>` that appends its stdin to a file, never the real hooks;
  use a scratch `CLINE_DIR` and `CLINE_MCP_SETTINGS_PATH`, never `$HOME/.cline`.
- **Capabilities**: one line per scope and id, `<scope> <id> <status> [<probe-date> <recipe>]`; the Go table is
  `internal/provisioning/capabilities.go`.

```capabilities
global skill unverified
global mcp unverified
global agent.delegate unverified
global agent.invoke unverified
global hook.PostInvocation absent
global hook.PostToolUse unverified
global hook.PreCompact absent
global hook.PreInvocation absent
global hook.PreToolUse unverified
global hook.SessionStart unverified
global hook.Stop absent
global hook.SubagentStop absent
global hook.UserPromptSubmit unverified
project skill absent
project mcp absent
project agent.delegate absent
project agent.invoke absent
project hook.PostInvocation absent
project hook.PostToolUse absent
project hook.PreCompact absent
project hook.PreInvocation absent
project hook.PreToolUse absent
project hook.SessionStart absent
project hook.Stop absent
project hook.SubagentStop absent
project hook.UserPromptSubmit absent
```
