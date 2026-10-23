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

`TestHarnessLedgerCoversEveryProvider` requires one `## <provider-id>` section per
provider in `configurator.Providers()` and no section for an unknown one.

Conventions: `unknown` means no version or date was ever recorded in this
repository; `unverified` means a source was named but not confirmed to exist. A
value is never filled in from memory. Documentation is not evidence: a change that
would flip a matrix cell is confirmed with a probe
(`.claude/skills/cartographer-audit-clients/references/probes.md`), not from a changelog line.

**Project skills and instructions below a bound folder (probe 2026-10-21, D1019).** A project
skill and project instructions in a bound non-git folder `W`, asked back from `W`, from a git
repository `W/repo` and from a plain subfolder `W/plain`. Four clients of five look for project
skills only up to the repository root, so a folder binding gives the repositories below it the
tools (through the bridge, D737) but not the skills or agents: a repository a KB declares in its
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

- **Aligned with**: `2.1.286` — 2026-10-01. Documentary review of the changelog
  from 2.1.246 (about 40 releases) and of the vendor docs; one cell probed (the
  project `CLAUDE.md` vs `AGENTS.md` precedence, below), the others not re-probed.
- **Sources** (reachable on 2026-10-01): changelog
  <https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md>;
  version: `claude --version`, latest `npm view @anthropic-ai/claude-code version`
  (dist-tags `latest`, `next`, `stable`); docs <https://code.claude.com/docs/en/memory>,
  `/sub-agents`, `/skills`, `/hooks`, `/mcp`, `/settings` under the same prefix.
- **Depends on**:
  - Stdio bridge (D737): `cartographer mcp --provider claude` is spawned in the session's folder (probe 2026-10-17, Claude Code 2.1.302: working directory yes, `roots` declared with `[cwd]`). The token variable reaches the child (the process environment is inherited). Replaces the D717 project-entry shadowing, which is no longer used.
  - Writer header (D904): an HTTP entry carries `Cartographer-Author` in `headers`, next to `Authorization`.
  - `skill`/`agent`/`hook` in `~/.claude/{skills,agents,hooks}/`, project-local in
    `.claude/…` (D137, D193); `mcp` in `~/.claude.json` (project: `.mcp.json`);
    `instructions` in `~/.claude/CLAUDE.md` (project: `CLAUDE.md`).
    Under `CLAUDE_CONFIG_DIR` only the `cartographer` MCP entries follow `clients.claude.config_dirs`
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

## codex

- **Aligned with**: `0.162.0` — 2026-10-09 for the write-findings probe (#637,
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
    layer is inactive unless the project is trusted (D193).
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

## kiro

- **Aligned with**: `2.28.0` — 2026-10-09 for the write-findings probe (#640,
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
  - Stdio bridge (D737): spawned in the session's folder (probe 2026-10-17, Kiro CLI 2.32.0); `roots` not declared. Kiro IDE not probed: an IDE that spawns the server outside the workspace and declares no roots gets the provider's default set.
  - Writer header (D904): none. Kiro documents `headers` for a remote server (<https://kiro.dev/docs/mcp/configuration/>), but the emitter writes no headers for Kiro, `Authorization` included (an older gap), so a write over a Kiro HTTP entry is authored by the token; the bridge names the writer.
  - `agent` as JSON (`name`/`description`/`prompt`) in `~/.kiro/agents/`,
    workspace `.kiro/agents/` wins over global (D195); native restriction keys
    believed to be `tools`/`allowedTools` (D291, not verified). The ledger names no
    read-only tool set, so the repository's read-only agents list `["*"]` and keep the
    restriction in the prompt; an agent with no `tools` has none (D300).
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
    `~/.kiro/settings/mcp.json`, `instructions` in `~/.kiro/steering/cartographer.md`.
  - session transcripts, read by the usage scan (D326): `~/.kiro/sessions/**/*.jsonl`.
    When the model activates a skill it reads its `SKILL.md` with the built-in
    file-read tool, recorded as `kind.BuiltIn.FileRead.operations[].path`; a shell
    record carries the path in its command. The scan reads only the `kind` object. The
    record shape is pinned by `internal/provisioning/testdata/usage/kiro-session.jsonl`
    (home replaced by `$HOME`). It was observed on the operator's machine on
    2026-10-05; **the Kiro version was not recorded then, so none is declared here**:
    re-probe on a machine with Kiro, read the shape off a real record, and write the
    version on this line.
- **Watch items**:
  - the default `kiro-cli chat` starts firing standalone hooks (its KAS log shows
    `v2 hooks cache initialized`). Then drop the `sessionHookLimit` on the Kiro
    hook mechanism, and the timer advice with it;
  - the usage scan's record shape (above) changes: a new Kiro release that renames
    `kind.BuiltIn.FileRead` makes every Kiro skill look unused, silently;
  - Kiro IDE: does it read a global `~/.kiro/hooks`, and fire a `SessionStart`
    from it?
  - Kiro Crew: does `~/.kiro/crew/hooks/` take the same v1 file?

  Probe both on a machine that has them before any matrix cell claims them.
- **Probe notes**: run the real binary
  (`"/Applications/Kiro CLI.app/Contents/MacOS/kiro-cli"` on macOS; the Homebrew
  symlink breaks its launcher). The V3 TUI asks before running an MCP tool, and a
  scripted `\r` does not answer it; use `-a` and accept its warning (down arrow,
  then Enter) before typing the prompt. A first run on a clean profile creates
  `~/.kiro/{logs,sessions,session-index,agents}`: remove them afterwards. Three agent engines coexist (`--agent-engine
  v1|v2|v3`, default `v2`): a feature present in a non-default engine is not
  "supported". An interactive probe needs a pty. Details in the probe recipe.

## opencode

- **Aligned with**: `2.0.30` — 2026-10-17, probed: project agents in `.opencode/agents/`
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
    generated JS plugin in `~/.config/opencode/plugins/` (documented, autoloaded;
    D59; shape per installed major, 1.x and 2.x, D359: 2.x rejects the 1.x shape; an unreadable version writes the newest shape with a warning, D719;
    sources `opencode.ai/v2/docs/build/plugins/` and `/migrate-v1`), files kept in `~/.opencode/hooks/` (never discovered by the client, run by path); sync layer 1 is the documented
    `session.created` event.
  - `mcp` under the `mcp` key of `opencode.json`; `instructions` in
    `~/.config/opencode/AGENTS.md` (both documented).
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
  - `opencode --version` prints `opencode v2.0.25` (seen on both streams); the major parser reads it (D719). The no-`PATH` sync run and `session.created` delivery to `ctx.event.subscribe` are not yet probed.
  - 2.x `opencode.json` is translated on load (`mcp` → `mcp.servers`,
    `permission` map → `permissions` list), so the 1.x shape still works.
- **Watch items**: the project skill cell (`.opencode/skills`) is documented. The
  project agent cell in `projectDestinationMatrix` is `.opencode/agent`, while the
  2.0.30 probe listed project agents from `.opencode/agents/`: the matrix cell and
  the probe disagree.
- **Probe notes**: 2.x has no `opencode agent list`: use `opencode debug agents`
  (JSON) and `opencode debug paths`. A background service caches the
  configuration: run `opencode reload` after changing a file. `opencode run -m
  opencode/big-pickle` works without credentials, but the free tier refuses some
  custom primary agents ("can only be used from within OpenCode").

## antigravity

- **Aligned with**: CLI `agy` `1.3.2` — 2026-10-09, probed (#478, #639). App
  `2.19.1` — 2026-10-01 (documentary review; the app was not available for the
  probe). The CLI is versioned separately (`agy --version`).
- **Sources**: changelog <https://antigravity.google/docs/changelog/>; CLI
  changelog
  <https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md>;
  docs <https://antigravity.google/docs/rules> (`/docs/rules-workflows/` redirects
  there), `/docs/skills`, `/docs/subagents`, `/docs/hooks`, `/docs/mcp`; installed
  version source: `unverified`.
- **Depends on** (what the matrix encodes; D193/D207, not re-probed):
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

## crush

- **Aligned with**: `v0.98.0` — 2026-10-09, probed (#478).
- **Sources**: releases <https://github.com/charmbracelet/crush/releases>; README
  <https://github.com/charmbracelet/crush>; config
  <https://github.com/charmbracelet/crush/tree/main/docs/config>; hooks
  <https://github.com/charmbracelet/crush/tree/main/docs/hooks>; JSON schema
  <https://charm.land/crush.json>; package: Homebrew tap
  `charmbracelet/tap/crush`, npm `@charmland/crush` (not in homebrew-core).
- **Depends on**:
  - Stdio bridge (D737): spawned in the session's folder (probe 2026-10-17, Crush 0.98.1); `roots` declared with an empty list, so the working directory decides.
  - Writer header (D904): an HTTP entry carries it in `headers`; a value Crush would expand (`$VAR`, `$(cmd)`) is written in the encoded form, never as is.
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

## copilot

- **Aligned with**: `1.0.98` — 2026-10-17, probed: project agents in
  `.github/agents/<name>.agent.md` load once the folder is trusted, the `model` key is
  honoured and an unavailable model falls back to `auto` with a warning.
  `1.0.94` — 2026-10-10, probed (#676). The GitHub Copilot CLI
  (`copilot`), not Copilot in VS Code.
- **Sources** (`unverified`): releases <https://github.com/github/copilot-cli/releases>;
  docs <https://docs.github.com/copilot/how-tos/use-copilot-agents/use-copilot-cli>.
  Every cell below comes from a probe with `COPILOT_HOME` pointed at a scratch
  directory, not from these pages.
- **Depends on**:
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
