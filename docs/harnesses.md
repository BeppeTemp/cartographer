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

- **Aligned with**: `2.1.286` — 2026-10-01. Documentary review of the changelog
  from 2.1.246 (about 40 releases) and of the vendor docs; one cell probed (the
  project `CLAUDE.md` vs `AGENTS.md` precedence, below), the others not re-probed.
- **Sources** (reachable on 2026-10-01): changelog
  <https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md>;
  version: `claude --version`, latest `npm view @anthropic-ai/claude-code version`
  (dist-tags `latest`, `next`, `stable`); docs <https://code.claude.com/docs/en/memory>,
  `/sub-agents`, `/skills`, `/hooks`, `/mcp`, `/settings` under the same prefix.
- **Depends on**:
  - `skill`/`agent`/`hook` in `~/.claude/{skills,agents,hooks}/`, project-local in
    `.claude/…` (D137, D193); `mcp` in `~/.claude.json` (project: `.mcp.json`);
    `instructions` in `~/.claude/CLAUDE.md` (project: `CLAUDE.md`).
  - Agent translation is a passthrough: Cartographer subagents are Claude
    subagents; Claude takes its own restriction fields (`tools`, also
    `disallowedTools`, `permissionMode`) at the top level (D291).
  - Sync layer 1: `SessionStart` entry in `~/.claude/settings.json`
    (`docs/sync.md` §Layer 1); hooks run through Git Bash on Windows, PowerShell
    as the documented fallback (D267).
  - `CLAUDE.md` → `@AGENTS.md` import and per-directory loading (D213). Since
    2.1.277 the built-in `agents-md@builtin` plugin (on by default) reads a
    project `AGENTS.md` itself, but **only when no `CLAUDE.md`/`CLAUDE.local.md`
    is on the project path**; the import stays necessary wherever a `CLAUDE.md`
    exists.
- **Watch items**:
  - [#476](https://github.com/BeppeTemp/cartographer/issues/476) (plan): the
    project `instructions` cell writes `./CLAUDE.md`, which hides the repository's
    `AGENTS.md` from Claude. Probed on 2.1.286 (plugin enabled): `AGENTS.md` alone
    → read; plus a `./CLAUDE.md` or `.claude/CLAUDE.md` holding only the managed
    block → not read; plus `@AGENTS.md` in that `CLAUDE.md` → read.
  - claude.ai account-synced skills share `~/.claude/skills/` (2.1.275, 2.1.280);
    not a cell.
- **Probe notes**: the AGENTS.md probe needs the plugin on: a machine can disable
  it (`"agents-md@builtin": false` in `~/.claude/settings.json`), so pass
  `--settings '{"enabledPlugins":{"agents-md@builtin":true}}'` to `claude -p` to
  test the default. Ask for a token planted in the file under test.

## codex

- **Aligned with**: `0.159.3` — 2026-10-01 (documentary review; cells not
  re-probed). Last probed: `0.153.4`, date `unknown` (D192: `codex debug
  prompt-input` catalogues skills in `~/.codex/skills`).
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
- **Watch items**: no issue open. To re-probe at the next cell pass: the
  `SessionStart` sentinel after 0.155.0 (session-start hooks now distinguish
  forked sessions), and the untrusted-project `.codex/` layer after the
  folder-trust rework of 0.156.0–0.158.0.
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

- **Aligned with**: `1.18.34` — 2026-10-01 (documentary review, client not
  installed, cells not re-probed; no release from 1.18.21 touches a Cartographer
  surface). Last probed: `1.18.20`, date `unknown` (D192).
- **Sources**: repository <https://github.com/anomalyco/opencode> (`sst/opencode`
  redirects there), releases <https://github.com/anomalyco/opencode/releases>,
  changelog <https://opencode.ai/changelog>; version: npm `opencode-ai`; docs
  <https://opencode.ai/docs/agents>, `/skills`, `/plugins`, `/mcp-servers`,
  `/rules`, `/config`.
- **Depends on**:
  - `agent` global `~/.opencode/agent/` (deliberate divergence, D192, guarded by
    `clientcompat_test.go`; the docs name `~/.config/opencode/agents/` and
    `.opencode/agents/`, and accept `agent/` for backwards compatibility):
    Markdown with `description` + `mode: subagent`. Native restriction key
    `permission` (`edit`, `bash`, … allow/ask/deny) is documented (D291); `tools`
    is no longer shown in the agents doc.
  - `skill` project `.opencode/skills/` (documented). Global `~/.opencode/skills/`
    is not in the documented global list (`~/.config/opencode/skills`,
    `~/.claude/skills`, `~/.agents/skills`): `unverified`. Hooks fire through a
    generated JS plugin in `~/.config/opencode/plugins/` (documented, autoloaded;
    D59), files kept in `~/.opencode/hooks/`; sync layer 1 is the documented
    `session.created` event.
  - `mcp` under the `mcp` key of `opencode.json`; `instructions` in
    `~/.config/opencode/AGENTS.md` (both documented).
- **Watch items**: no issue open. Probe the undocumented global
  `~/.opencode/skills/` when the client is installed; 1.18.24 introduced a V2
  config format (watch the `mcp` key shape).
- **Probe notes**: `opencode agent list` and `opencode debug config` print what the
  client discovers; `debug config` redacts credentials and sensitive headers since
  1.18.33.

## hermes

- **Aligned with**: `v2026.9.24` (v0.21.5) — 2026-10-01 (documentary review,
  client not installed, cells not re-probed).
- **Sources**: releases <https://github.com/NousResearch/hermes-agent/releases>;
  docs <https://hermes-agent.nousresearch.com/docs/> (skills, curator, hooks, mcp,
  personality under `/docs/user-guide/features/`); version: `hermes --version`.
  The upstream was identified from the repository's own vocabulary
  (`$HERMES_HOME`, `skill_manage`, curator, `SOUL.md`), not from a cited URL.
- **Depends on**:
  - `skill` only, **delivered** to `$HERMES_HOME/skill-inbox/<name>/cartographer/`
    for the agent to adopt, never installed (D141). The `skill-inbox/` convention
    is `unverified` upstream: the curator docs say external skills have no
    dedicated inbox and adoption is `hermes curator adopt` (v2026.8.3). The four
    other kinds are unsupported for stated reasons (`config.yaml` rendered by an
    Ansible role, `SOUL.md` operator-owned).
  - No project-local cell; no session hook: sync layer 1 is the scheduled timer
    (D140/D141).
- **Watch items**: no issue open. The docs now describe surfaces D141 states as
  absent; none probed, so no cell changes:
  - gateway hooks in `$HERMES_HOME/hooks/<name>/` (`HOOK.yaml` + `handler.py`,
    `session:start`, documented as gateway-only) and shell hooks in the
    `config.yaml` `hooks:` block;
  - project-local skills in `.hermes/skills/` and `.agents/skills/` (after
    `hermes skills trust`) and a project `AGENTS.md`;
  - `skills.external_dirs` in `config.yaml` as an alternative to the inbox.
- **Probe notes**: the skill probe checks the inbox, and that `$HERMES_HOME/skills/`
  is untouched; the agent's adoption may be gated by `skills.write_approval` and
  by protected-file approval (v2026.8.31). Hooks and project-local skills need a
  sentinel plus an untrusted negative control.

## antigravity

- **Aligned with**: `2.19.1` — 2026-10-01 (documentary review, client not
  installed, cells not re-probed). The Antigravity CLI is a separate product,
  versioned separately (`1.2.14` at review time).
- **Sources**: changelog <https://antigravity.google/docs/changelog/>; CLI
  changelog
  <https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md>;
  docs <https://antigravity.google/docs/rules> (`/docs/rules-workflows/` redirects
  there), `/docs/skills`, `/docs/subagents`, `/docs/hooks`, `/docs/mcp`; installed
  version source: `unverified`.
- **Depends on** (what the matrix encodes; D193/D207, not re-probed):
  - Global only, under `~/.gemini`: `skill` `config/skills`, `agent`
    `config/agents` (Markdown: `name`, `description`, `mainAgent: false`,
    `subagent: true`), `hook` `config/hooks` registered in
    `~/.gemini/config/hooks.json`, `mcp` `config/mcp_config.json`, `instructions`
    `GEMINI.md`. No project-local cell of any kind.
  - Native hooks exist but there is no `SessionStart` event (documented:
    PreToolUse, PostToolUse, PreInvocation, PostInvocation, Stop): sync layer 1 is
    the timer (D140, D194, D284). Agent frontmatter documents `tools`, `model`
    and `commandExecutionPolicy` (candidate D291 keys, not verified).
- **Watch items**: no issue open. The docs now describe a workspace scope that
  would flip several cells if a probe confirms it: `.agents/skills/`,
  `.agents/agents/`, `.agents/hooks.json`, `.agents/mcp_config.json`,
  `.agents/rules/*.md`, native `AGENTS.md`/`GEMINI.md` reading up to the
  workspace root, and repository settings in `.gemini/config.json` (2.17.0). The
  CLI uses `~/.gemini/antigravity-cli/{skills,rules}` rather than `config/*`. A
  `SessionStart` event would change the layer-1 trigger.
- **Probe notes**: a workspace probe plants a token in each `.agents/*` surface and
  repeats in a directory without `.agents/` (negative control), on both the app
  and the CLI; needs an install.

## crush

- **Aligned with**: `v0.97.1` — 2026-10-01 (documentary review, client not
  installed, cells not re-probed).
- **Sources**: releases <https://github.com/charmbracelet/crush/releases>; README
  <https://github.com/charmbracelet/crush>; config
  <https://github.com/charmbracelet/crush/tree/main/docs/config>; hooks
  <https://github.com/charmbracelet/crush/tree/main/docs/hooks>; JSON schema
  <https://charm.land/crush.json>; package: Homebrew tap
  `charmbracelet/tap/crush`, npm `@charmland/crush` (not in homebrew-core).
- **Depends on**:
  - `mcp` in `~/.config/crush/crush.json` — JSON Crush documents as deprecated (no
    new options) in favour of `crushrc`, a Bash file Cartographer refuses to
    write; `$VAR` form for values, `$(` refused (D225). JSON is still read and
    merged with `crushrc`.
  - `instructions` in `~/.config/crush/CRUSH.md` (Crush also loads
    `~/.config/AGENTS.md`); `skill` in `~/.config/crush/skills/` (project
    `.crush/skills/`; Crush also scans `~/.claude/skills`, `~/.agents/skills` and
    the `.agents`/`.claude`/`.cursor` project skill directories).
  - `agent` unsupported (no documented subagent mechanism); sync layer 1 is the
    timer.
- **Watch items**: no issue open. Documented since D225, not probed:
  - hooks: a `hooks` map in `crush.json`/`.crush.json` (global and project), one
    event, `PreToolUse` (`{name, matcher, command, timeout}`, Claude
    Code-compatible payload) — D225's "no hook mechanism is documented" no longer
    holds; a probe plus a decision on mapping a per-name `hook` artifact to a JSON
    map entry are needed, and `PreToolUse` cannot replace the session trigger;
  - project JSON configuration (`.crush.json`/`crush.json` next to `.crushrc`):
    would allow the project `mcp`/`hook` cells (D193/D225);
  - `crush.json` becoming unreadable, or `crushrc` the only format, would remove
    the `mcp` cell.
- **Probe notes**: none recorded; client not installed.
