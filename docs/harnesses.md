# Harness ledger

Per agent client ("harness"), the client version Cartographer was last aligned
with, where that client announces changes, and which parts of the provider matrix
depend on it. The matrix itself stays the source of truth for *what* is written
where (`destinationMatrix` in `internal/provisioning/provisioning.go`,
`projectDestinationMatrix` in `internal/provisioning/workspacescope.go`, the
descriptors in `internal/configurator/registry.go`); this page records *how
recently it was checked* and *what to watch*. The `harness-watch` skill
(`.agents/skills/harness-watch/SKILL.md`) reads it as its baseline and updates it
as its output ([D292](decisions/D292-a-harness-ledger-and-a-procedure-not-version-polling.md)).

`TestHarnessLedgerCoversEveryProvider` requires one `## <provider-id>` section per
provider in `configurator.Providers()` and no section for an unknown one.

Conventions: `unknown` means no version or date was ever recorded in this
repository; `unverified` means a source was named but not confirmed to exist. A
value is never filled in from memory. Documentation is not evidence: a change that
would flip a matrix cell is confirmed with a probe
(`.agents/skills/harness-watch/references/probes.md`), not from a changelog line.

## claude

- **Aligned with**: `unknown` — no client version was ever recorded for Claude Code.
- **Sources**: vendor documentation and changelog: `unverified` (no URL is cited
  in the repository); version: `claude --version`.
- **Depends on**:
  - `skill`/`agent`/`hook` in `~/.claude/{skills,agents,hooks}/`, project-local in
    `.claude/…` (D137, D193); `mcp` in `~/.claude.json` (project: `.mcp.json`);
    `instructions` in `~/.claude/CLAUDE.md` (project: `CLAUDE.md`).
  - Agent translation is a passthrough: Cartographer subagents are Claude
    subagents; Claude takes its own restriction fields at the top level (D291).
  - Sync layer 1: `SessionStart` entry in `~/.claude/settings.json`
    (`docs/sync.md` §Layer 1); hooks run through Git Bash on Windows (D267).
  - `CLAUDE.md` → `@AGENTS.md` import and per-directory loading (D213).
- **Watch items**: none open.
- **Probe notes**: none specific.

## codex

- **Aligned with**: `0.153.4` — date `unknown` (D192: `codex debug prompt-input`
  catalogues skills in `~/.codex/skills`).
- **Sources**: documentation: <https://developers.openai.com/codex/skills> and
  <https://developers.openai.com/codex/guides/agents-md> (both cited in the
  code); changelog and version source: `unverified`.
- **Depends on**:
  - `skill` global `~/.codex/skills/` (deliberate divergence from the documented
    `$HOME/.agents/skills`, D192, guarded by `clientcompat_test.go`); project
    `.agents/skills/` (D193).
  - `agent` as TOML (`name`/`description`/`developer_instructions`) in
    `~/.codex/agents/`; native restriction key believed to be `sandbox_mode`
    (D291, not verified against the client).
  - `hook` files in `~/.codex/hooks/`, registered in `~/.codex/hooks.json`
    (D230); sync layer 1 `SessionStart`.
  - `mcp` and `instructions` in `~/.codex/config.toml` and `~/.codex/AGENTS.md`;
    hook trust is keyed by file, and a project's `.codex/` layer is inactive
    unless the project is trusted (D193).
- **Watch items**: none open.
- **Probe notes**: `codex debug prompt-input` lists the catalogued skills.

## kiro

- **Aligned with**: `2.26.1` — 2026-10-01 for the hook probe only (default v2
  engine, `--agent-engine v3`, `--v3 --tui`; KAS `0.66.15`); `2.21.3` —
  2026-09-11 for the agent and hook cells (D195); `2.21.4` for skill symlinks
  (D207, `CONTRIBUTING.md`); `2.20.0` — 2026-08-27 for D140.
- **Sources**: changelog <https://kiro.dev/changelog/cli/>; hook docs
  <https://kiro.dev/docs/hooks/> and <https://kiro.dev/docs/cli/v3/hooks-migration/>
  (all cited in D195/D140); version: `brew info --cask kiro-cli`.
- **Depends on**:
  - `agent` as JSON (`name`/`description`/`prompt`) in `~/.kiro/agents/`,
    workspace `.kiro/agents/` wins over global (D195); native restriction keys
    believed to be `tools`/`allowedTools` (D291, not verified).
  - `hook` is **unsupported** (D140, D195): standalone hooks exist in the shipped
    binary (KAS) but fire only in the interactive V3 UI; sync layer 1 stays the
    scheduled timer.
  - `skill` in `~/.kiro/skills/` (project `.kiro/skills/`), `mcp` in
    `~/.kiro/settings/mcp.json`, `instructions` in `~/.kiro/steering/cartographer.md`.
- **Watch items**: [#265](https://github.com/BeppeTemp/cartographer/issues/265).
  Trigger: V3 becomes the default interactive engine (the default session stops
  advertising it as an early release). Then a `SessionStart` hook in
  `~/.kiro/hooks/` is the layer-1 trigger; the non-interactive gap still needs the
  timer. Re-check of 2026-10-01: standalone hooks fire only under
  `chat --v3 --tui`, never in `--no-interactive` runs nor the default engine;
  `AgentSpawn` and `PromptSubmit` are not valid KAS triggers.
- **Probe notes**: run the real binary
  (`"/Applications/Kiro CLI.app/Contents/MacOS/kiro-cli"` on macOS; the Homebrew
  symlink breaks its launcher). Three agent engines coexist (`--agent-engine
  v1|v2|v3`, default `v2`): a feature present in a non-default engine is not
  "supported". An interactive probe needs a pty. Details in the probe recipe.

## opencode

- **Aligned with**: `1.18.20` — date `unknown` (D192).
- **Sources**: documentation <https://opencode.ai/docs/agents> (cited in the code);
  changelog and version source: `unverified`.
- **Depends on**:
  - `agent` global `~/.opencode/agent/` (deliberate divergence from the documented
    `.opencode/agents`, D192, guarded by `clientcompat_test.go`): Markdown with
    `description` + `mode: subagent`; native restriction keys believed to be
    `permission`/`tools` (D291, not verified).
  - `skill`/`hook` in `~/.opencode/{skills,hooks}/`; hooks fire through a generated
    JS plugin in `~/.config/opencode/plugins/` (D59); sync layer 1 is the
    `session.created` event.
  - `mcp` in `opencode.json`, `instructions` in `~/.config/opencode/AGENTS.md`.
- **Watch items**: none open.
- **Probe notes**: `opencode agent list` and `opencode debug config` print what the
  client discovers.

## hermes

- **Aligned with**: `unknown` — no client version was ever recorded.
- **Sources**: `unverified` (no URL is cited in the repository).
- **Depends on**:
  - `skill` only, and **delivered** to `$HERMES_HOME/skill-inbox/<name>/cartographer/`
    for the agent to adopt, never installed (D141); the four other kinds are
    unsupported for stated reasons (config rendered by an Ansible role, `SOUL.md`
    operator-owned).
  - No project-local cell; no session hook: sync layer 1 is the scheduled timer
    (D140/D141).
- **Watch items**: none open.
- **Probe notes**: the skill probe checks the inbox, and that `$HERMES_HOME/skills/`
  is untouched.

## antigravity

- **Aligned with**: `unknown` — no client version was ever recorded.
- **Sources**: documentation <https://antigravity.google/docs/rules-workflows/>
  (cited in the code); changelog and version source: `unverified`.
- **Depends on**:
  - Global only, under `~/.gemini`: `skill` `config/skills`, `agent`
    `config/agents` (Markdown: `name`, `description`, `mainAgent: false`,
    `subagent: true`), `hook` `config/hooks` registered in
    `~/.gemini/config/hooks.json`, `mcp` `config/mcp_config.json`, `instructions`
    `GEMINI.md`. No project-local cell of any kind (D193).
  - Native hooks exist but there is no `SessionStart` event: sync layer 1 is the
    timer (D140, D194); `PreInvocation`/`PostInvocation` are its own events
    (D284). Native restriction keys per D291 are not verified.
- **Watch items**: none open. A project-local configuration root or a
  `SessionStart` event would flip cells.
- **Probe notes**: none recorded.

## crush

- **Aligned with**: `unknown` — no client version was ever recorded (D225 was
  written from the README and docs, not from a run).
- **Sources**: <https://github.com/charmbracelet/crush> (README sections cited in
  the code: configuration, allowing tools, global context files, agent skills);
  changelog and version source: `unverified`.
- **Depends on**:
  - `mcp` in `~/.config/crush/crush.json` — the format Crush documents as
    deprecated in favour of `crushrc`, a Bash file Cartographer refuses to write;
    `$VAR` form for values, `$(` refused (D225).
  - `instructions` in `~/.config/crush/CRUSH.md`; `skill` in
    `~/.config/crush/skills/` (project `.crush/skills/`).
  - `agent` and `hook` unsupported (no documented mechanism); sync layer 1 is the
    timer.
- **Watch items**: none open. A documented subagent directory, a hook mechanism or
  a `crushrc`-only configuration would flip cells.
- **Probe notes**: none recorded.
