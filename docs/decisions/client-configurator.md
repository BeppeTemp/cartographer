# Client and configurator decisions

Client CLI/TUI behavior and provider-specific configuration. Current behavior: [`../configurator.md`](../configurator.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d23"></a>
## D23 — Multi-provider configurator: CLI flags + per-provider JSON adapters, non-destructive merge
**Decision.** The configurator accepts CLI flags (`--name`, `--transport`, `--url`, `--auth`, `--token-env`) with sensible defaults (`DefaultConfig()`) and generates the MCP configuration files for Claude Code (`.claude.json`, `mcpServers` key), Codex CLI (`.codex/config.json`), Kiro (`.kiro/settings/mcp.json`), and OpenCode (`.opencode/config.json`). Claude Code reads `mcpServers` from `~/.claude.json`, not from a separate `.claude/mcp_servers.json` file. Merging with existing files is non-destructive: entries for other servers are preserved. Implemented as the `internal/configurator` package + the `cmd/configure` binary.
**Rationale.** CLI flags are the simplest and most composable configuration point — no YAML file to maintain in the KB, no extra parsers. The non-destructive merge is essential to avoid overwriting the user's existing configurations. The JSON format uses stdlib `encoding/json`. The `mcp/wiki.yaml` (previously the source of truth) was removed together with the `mcp/` and `raw/` directories from the KB structure (June 2026, D28). *(Superseded by D37: `cmd/configure` was deleted — the client subcommands now live in `cmd/cartographer`, HTTP-only transport.)* *(Bug discovered in D58: `.codex/config.json` was never the file Codex CLI reads — Codex reads only `config.toml`. `emitCodex` now generates managed-block TOML; see D58.)*

---

<a id="d29"></a>
## D29 — OpenCode format aligned to the official schema + `kb.Init` creates the git repo

### D29a — OpenCode format: `opencode.json`, `$schema`, `enabled`, `command` array
**Decision.** `emitOpenCode` in `internal/configurator/configurator.go` is aligned to the official OpenCode v1.17.10 schema (`https://opencode.ai/config.json`):
- The generated file is `opencode.json` (not `.opencode/config.json`).
- The root key `"$schema": "https://opencode.ai/config.json"` is always included.
- Each MCP entry includes `"enabled": true`.
- For stdio transport: `"command"` is an **array of strings** (not a scalar string).
- For remote transport with auth: `"headers"` uses OpenCode's native `{env:VAR}` syntax (not `${VAR}`).
**Rationale.** The previous format was based on incomplete documentation. The real schema (verified against an OpenCode v1.17.10 config) requires a `command` array and an explicit `enabled`. The `{env:VAR}` syntax for env vars diverges from Claude/Kiro's (`${VAR}`) and must be documented explicitly to avoid misconfigurations. Known risk: OpenCode is SSE-first and custom header support on remote MCP may require `mcp-remote`/`mcp-auth.json` (see `interoperability.md`).

### D29b — `kb.Init` initializes the KB as a git repository (best-effort)
**Decision.** `kb.Init` runs `gitx.Init` + initial commit "init: KB inizializzata" after creating the layout, only if the directory is not already a git repo (`gitx.IsRepo`). The git init is **best-effort**: if git is unavailable or the init fails, Init does not fail — the KB remains valid without git. `WriteConcept` does not auto-commit: commits remain an explicit operation (`commit_gate`).
**Rationale.** `interoperability.md` states that "each KB is its own git repository", but `Init` did not initialize the repo, leaving the KB non-git unless a manual step was taken. This caused `ErrNothingToCommit` on the first `commit_gate` and broke tools that assumed a git repo (e.g. `sync_check`). Best-effort ensures that tests and environments without git installed are not broken.

---

<a id="d35"></a>
## D35 — Configurator: opt-in interactive TUI (`--tui`)

**Decision.** The `cmd/configure` binary gains an interactive TUI mode activated by `--tui`, based on `github.com/charmbracelet/bubbletea` (+ `bubbles`, `lipgloss`). The generation business logic (Emit/Apply MCP config + `provisioning.BuildManifest`/`Apply` for skills) was extracted into a shared function `runConfigure(configureParams)`; both the flag mode (default, unchanged) and the TUI call it. The wizard collects KB/provider/transport/URL/auth/token-env/name/base-dir/dry-run with defaults from `DefaultConfig()` and a confirmation screen.

**Rationale.** AD4 called for the TUI **only** in the multi-provider configurator (never in the server): this decision implements it. The key condition is **zero duplication**: the TUI is a pure front-end that populates the same parameters as the flags and delegates to `runConfigure`, so the two modes cannot diverge. Flags remain the default (the TUI is opt-in) → no regression for scripted/CI uses. `bubbletea` is the idiomatic TUI library in Go and is pure-Go (no cgo). It adds transitive dependencies (lipgloss/bubbles/x), acceptable for an administrative binary separate from the server.

*(Superseded by D37: `cmd/configure` was deleted and the TUI recast as the flag-less dashboard of `cartographer` — `--tui` no longer exists, see `docs/configurator.md` §TUI mode.)*

---

<a id="d37"></a>
## D37 — Single binary with subcommands; client always HTTP (no stdio/`--check`/`--base-dir`)

**Decision.** `cartographer-configure` is eliminated: `cmd/cartographer` becomes a binary with
subcommands (`serve`, `version`, `help`, plus the client subcommands `agents`/`connect`/`status`/
`sync`); with no arguments it opens a TUI dashboard in a TTY. The client always talks to the server via
HTTP: the client-side stdio transport, `--check` (replaced by `status`), and `--base-dir`
(replaced by cwd/`--global`) are removed.
**Rationale.** Real deploy topologies (local Docker, multi-client k8s) have the client
on a different machine from the server, or in any case connected over the network: a "direct on the
filesystem" stdio transport was dead code duplicating the materialization logic. A single binary with
subcommands (`git`/`kubectl` style) is more discoverable than two binaries with different flags.
Details: `docs/configurator.md`, `docs/deployment.md` §Topologies.

---

<a id="d42"></a>
## D42 — `cartographer disconnect`: inverse of `connect`, inverse JSON merge, full provider prune

**Decision.** New subcommand `cartographer disconnect [provider|all]`, business logic
shared (`doDisconnect`) between CLI and TUI. Per provider: `configurator.Remove` (inverse of
`Apply`) removes only the server's entry from the MCP config file without destroying the rest;
`provisioning.PruneManaged` removes **all** the provider's managed files from the lockfile — not just
the diff against the manifest — because `disconnect` must not depend on the server being
reachable.
**Rationale.** Reuse instead of duplication: `PruneManaged`/`Remove` are the same functions used
by `Apply`, applied in the opposite direction. The "full" prune (the whole managed set) is the
fundamental difference from `sync`: disconnecting is a local operation.
Details: `docs/configurator.md` §`cartographer disconnect`.

---

<a id="d49"></a>
## D49 — Interactive `cartographer connect`: bubbletea form shared TUI/CLI

**Decision.** The connect form (server URL, name, token env var, auth toggle) is extracted into a
standalone bubbletea component, `connectFormModel` (`connectform.go`), reused unchanged by TUI and
CLI. A `standalone` field decides whether submit/cancel emit `tea.Quit` (command running its own
program) or stay no-op (form nested in the TUI, which reacts to `Submitted()`/`Cancelled()`).
`cmdConnect` opens the form (new `--no-input` flag as escape hatch) only if none of the four
form flags was passed explicitly and both stdin and stdout are a TTY
(`wantsConnectForm`, a pure function testable without a real `tea.Program`).
**Rationale.** The delicate constraint is "the nested form must never emit `tea.Quit`": without the
`standalone` field the TUI would exit every time the user closes the connect form. Same struct,
same `Update`, parametric quitting behavior instead of two parallel implementations.
Details: `docs/configurator.md` §`cartographer connect [provider|all]`, §TUI mode.

---

<a id="d51"></a>
## D51 — MCP server name fixed to "cartographer"; auto-trust hint with the exact command

**Decision.** The name under which the server is registered in the providers' MCP configs is no longer
configurable via flag/form: it is always **`cartographer`** (escape hatch: `server_name` in
`.cartographer.yaml`). Every "needs approval" message now prints the exact command to run
(`cartographer sync --auto-trust`) via `autoTrustCommand`, instead of the vague "use --auto-trust".
**Rationale.** The name was a knob with no real use cases that lengthened the form and risked
duplicate MCP entries on every rename.

*(Superseded state: `--global` removed from `autoTrustCommand`/everywhere → D52.)*

---

<a id="d52"></a>
## D52 — `.cartographer.yaml` always machine-wide (home): project/global scope removed

**Context.** Every client subcommand and the TUI had a `--global`/`g` flag/key to choose
between `.cartographer.yaml` in the cwd or in the home. The project scope was never used: the
connection to a server is a property of the machine, not of the repo in which the command runs.

**Decision.** `.cartographer.yaml`/the lockfile **always** live in `~/`. `clientconfig.
TargetDir()` no longer takes a `global` parameter. Removed everywhere: the `--global` flag, the `g`
key in the TUI, the `global` parameter from `printApplySummary`/`autoTrustCommand`.

**Discarded alternatives.** Keeping the flag with a `true` default (it would have left a dead
project-scope codepath to maintain for no benefit).
Details: `docs/configurator.md` §`.cartographer.yaml`.

---

<a id="d63"></a>
## D63 — Complete prune: empty directories with boundaries, MCP configs reduced to empty (WP7)

**Context.** A `connect`→`disconnect` round trip did not return the filesystem to its initial
state: `PruneManaged` ran `os.Remove` on the individual `ManagedFile` entries but never on the
containing directories left empty; `configurator.Remove` left `{"mcpServers": {}}` on disk once
the only entry was removed.

**Decision.**
1. **Empty directories (`pruneEmptyDirs`).** After removing a managed file, it walks up the
   parent directories deleting them if empty, always stopping at a known root
   (`.claude`, `.codex`, `.kiro`, `.opencode`, `.config`, `.config/opencode`, or `BaseDir`).
   `os.Remove` (never `os.RemoveAll`) fails on its own on a non-empty directory, so a user
   file/other artifact stops the ascent without an explicit check.
2. **MCP configs reduced to empty.** If the server map ends up empty, the key is deleted; for
   files dedicated solely to MCP config (kiro, opencode) the whole file is deleted if nothing
   else remains.
3. **Absolute invariant: `.claude.json` is never deleted** (shared state broader than
   Claude Code) — it stays reduced to `{}`, a residue accepted by construction.
4. **End-to-end test** (`TestRoundTrip_ConnectDisconnect_NessunResiduo`) verifies, with a
   `filepath.Walk` before/after, that the only difference is the set of exceptions above and that
   pre-existing user files survive.
**Discarded alternatives.** `os.RemoveAll` on the providers' directories (it would delete content not
managed by Cartographer); always deleting the MCP config file when the entry is removed (wrong
for `.claude.json`, broader shared state).
Details: `docs/configurator.md` §`cartographer disconnect`.

---

<a id="d64"></a>
## D64 — Connect UX: per-field hints, retry with populated form, persistent default, pre-connect probe (WP8)

**Context.** Four frictions in the `connect` flow: the "Token env" field mistaken for the token
itself; on error the typed values were lost (CLI exited with exit 2); `doDisconnect`
deleted `.cartographer.yaml`, so the next connect restarted from localhost even on
machines pointed at a real server; no validation at submit (errors discovered only at a
"deferred" sync).

**Decision.**
1. **Per-field hints** (`fieldHint`): "Token env var" label with a contextual hint clarifying it is
   the env var's *name*, not the token; ignored if Auth is off.
2. **Error → form re-presented with the entered values**: `errMsg`/`forceRetry` in `connectFormModel`,
   inline error, connect stays idempotent (no `disconnect` needed to retry).
3. **Persistent default server**: `doDisconnect` clears only `agents` in `.cartographer.yaml`,
   preserving `server_url`/`trust`/`kbs` as prefill; new env `CARTOGRAPHER_SERVER_URL`.
4. **Pre-connect probe with force-override**: new `client.Ping(timeout)` (JSON-RPC `ping`, 5s) at
   submit, before writing files; on failure the form returns with the error, but a second consecutive
   submit with no changes skips the probe and proceeds (server temporarily down with a valid
   config) — CLI equivalent via `y/N` prompt.
**Discarded alternatives.** Skipping the Token env field from the tab order with Auth off (complicates the
focus cycle for little gain); `tools/list`/`initialize` as probe (`ping` is cheaper);
blocking probe without override (it would prevent saving a correct config with the server down).
Details: `docs/configurator.md` §`cartographer connect [provider|all]`.
**Follow-up (July 2026).** Point 3 preserved `server_url` in `.cartographer.yaml`, but only the
*interactive form* re-read it as prefill: in the non-interactive path (`--no-input`, non-TTY)
`cmdConnect` built `opts` from the flags' defaults, so a bare `connect <agent>` on a machine
already pointed at a remote server rewrote the shared config to `http://localhost:8080` / `auth:false`.
Now the **flag > config > default** precedence also applies there: `resolveConnectSettings` (a pure,
tested function) inherits `server_url`/`auth`/`token_env` from the existing `.cartographer.yaml` for each flag not
passed explicitly.

---

<a id="d86"></a>
## D86 — Connect UX: agent subsets, 0-KB diagnostics, absolute paths

**Status: implemented (2026-07-24).**

**Context.** A healthy service with no mounted KB returned `400 kb parameter required` from
`/mcp`, which `connect` described as an unreachable local service. `connect` and `disconnect`
also accepted only one provider or `all`, despite the configurator already supporting all four
providers independently; and their success output showed paths relative to the target directory,
making a write look as if it had happened in the current directory.

**Decision.**
- **Health-aware probe.** `internal/client.Health` derives `/health` from the configured MCP URL
  and parses the additive `ready` and `kbs` fields defensively. A false `ready`, or an explicitly
  empty `kbs` list from a pre-D84 server, produces the first-KB guidance (`kb create`, then
  `service restart`); only an actually unreachable loopback service offers installation. A missing
  `ready`/`kbs` remains compatible with older healthy servers.
- **Provider subsets.** `connect` and `disconnect` accept `--agents claude,codex` as a validated
  comma-separated selection; it cannot be combined with the positional provider. The interactive
  Bubble Tea form exposes one checkbox per supported provider, preselected from the detected set.
- **Unambiguous output.** `configurator.Apply` records and returns absolute config paths while
  retaining relative paths for provider-native file lookup. A successful non-dry-run connect also
  tells the user to restart the selected agent sessions so their MCP clients reload the tools.

**Rationale.** Health is the appropriate readiness signal for a client-side onboarding decision:
it distinguishes a server that needs its first KB from one that is down without making `/mcp`'s
transport error carry product guidance. CSV and checkboxes cover the common partial-install case
without adding a new provider model, while absolute paths make the side effects of configuration
safe to verify from any working directory.

---

<a id="d92"></a>
## D92 — Per-KB MCP entries for multi-KB servers

**Status: implemented (2026-07-24).**

**Decision.** `connect` and `sync` enumerate mounted KBs from `GET /health`. A multi-KB server
emits one entry per KB, named `<server_name>-<kb>` and scoped with `?kb=<kb>`; the discovered list
is persisted in `.cartographer.yaml` and `sync` reconciles additions, removals and the
one↔many rename. A one-KB or older server retains the single bare `<server_name>` entry. Disconnect
removes both the bare entry and every persisted suffixed entry.

**Rationale.** Query routing already works on every server version that can mount multiple KBs,
whereas path routing is newer and not required by all clients. Separate agent-visible entries make
the KB choice explicit and prevent a second mounted KB from turning an existing bare `/mcp` entry
into a 400, without introducing a client-side multiplexing protocol.

---

<a id="d99"></a>
## D99 — Codex's `config.toml`: comment markers are not enough, orphaned tables are adopted

**Decision.** Before rewriting one of its managed blocks in `~/.codex/config.toml`, Cartographer removes from the rest of the file every table that block owns: the `[mcp_servers.<name>]` tables declared in the block being written (`configurator.Apply`, `CodexMCPTableOwner`) and the `[[hooks.<event>]]` registrations whose `command` points inside `.codex/hooks/<name>/` (`registerHookConfigTOML`, `codexHookTableOwner`). The scan (`internal/configurator/codextoml.go`) stays purely textual, as D58 requires: it recognizes table headers, folds each header's sub-tables into its span, skips anything inside *any* Cartographer block, and leaves every other byte — comments, ordering, unrelated tables — untouched. Each adoption is reported as a warning on `connect`/`sync`.

**Rationale.** D58's ownership model (comment markers + `internal/blocktext`) assumes the markers survive. They do not: Codex CLI re-serializes the whole `config.toml` whenever it persists its own settings (trusted hook hashes, `[tui]`, `[projects.*]`), emitting the tables in canonical form and dropping every comment. The tables survive, the markers do not, and `blocktext.Write` — which appends when it finds no markers — then declares `[mcp_servers.cartographer]` a second time: a duplicate key, so `codex` refuses to start. For hooks the file stays valid (an array-of-tables may repeat) and the hook simply fires twice, which is quieter and worse. Parsing the file as TOML would fix it and lose exactly what D58 exists to protect, so ownership is instead resolved by identity: a table we would write, outside every block of ours, is a copy of ours.

**Consequences.** `connect`/`sync` self-heal an already-broken machine on the next run: no hand-editing, and nothing on the client's read path parses `config.toml`, so a duplicated file never blocks the repair. `[hooks.state."…"]` entries are deliberately **not** pruned: they are Codex's own bookkeeping (a trusted hash per hook, keyed by position), a stale one is inert because Codex gates it on a hash we do not compute, and deleting them would mean interpreting Codex's internals — the very format-awareness D58 rules out. `EnsureBootstrapHook` has no warnings channel and drops its repair message; the same repair on the MCP entry is reported, which is what makes the file visibly change. Table identity is the reason hooks are matched by command path and not by header: `[[hooks.PreToolUse]]` is shared by every hook on that event.

---

<a id="d113"></a>
## D113 — One client status snapshot across CLI and dashboard

**Status: implemented (2026-07-28).**

**Decision.** Client status is collected once into a versioned,
renderer-independent snapshot. Table commands, JSON output and the Bubble Tea
dashboard consume that snapshot. A failed endpoint request produces one
classified server error and marks connected provider state unknown; JSON keeps
the wrapped cause while human output gives the endpoint and an actionable next
step. Command discovery is grouped and conservative, and the dashboard adapts
its presentation and visible actions to terminal width and selection.

**Rationale.** Independent render paths had drifted into separate network calls
and contradictory repeated errors. A small stable schema gives scripts a safe
contract while keeping the terminal interface concise, and makes the dashboard
an alternate view of the same facts rather than a second status implementation.

---

<a id="d126"></a>
## D126 — Codex's `config.toml`: foreign tables written inside a managed block are relocated, not lost

**Status: implemented (2026-08-04).**

**Decision.** Before either Codex `config.toml` block rewrite, Cartographer moves out of the
span every top-level table group that the block's next contents do not themselves declare
(`configurator.EvictForeignTablesFromBlock`, `internal/configurator/codextoml.go`, sibling of
D99's `AdoptCodexOrphanTables`). Foreign groups are cut from the span, verbatim, and re-inserted,
in file order, immediately before the begin marker line, separated from their neighbours by at
most one blank line (`joinSeam`); a key declared both inside the span and in the block's next
contents is left alone — it is ours, `blocktext.Write` will overwrite it. Both call sites
(`registerHookConfigTOML` in `internal/provisioning/hooksettings.go`, and the `.toml` branch of
`configurator.Apply`) run the new eviction *before* D99's adoption and before `blocktext.Write`,
so a table that is both foreign-in-span and adoption-owned collapses to one copy instead of a
duplicate key. Purely textual, as D58 requires: no parse/re-serialize, every byte outside the
moved spans preserved.

**Rationale.** D99 made the write path idempotent against Codex's own rewrites by adopting the
marker-less copies of tables Cartographer owns. It did not cover the mirror case: Codex records
its per-hook trusted-hash bookkeeping (`[hooks.state."<config path>:<event>:<i>:<j>"]`) positionally
after the last `[[hooks.*]]` table in the file — which, in a Cartographer-managed `config.toml`, is
the one inside our own span. `blocktext.Write` replaces everything between its markers, so those
tables were silently destroyed on the next `connect`/`sync`, even though `codexHookTableOwner`
(D99) deliberately excludes `[hooks.state]` from adoption specifically to leave Codex's own
bookkeeping alone — the write path was destroying what the adoption predicate was written to
protect. Rewriting the relocated tables in canonical form would mean parsing/re-serializing
`config.toml`, which D58 forbids; relocating them verbatim keeps the invariant that Cartographer
owns only the text it wrote.

**Consequences.** Codex no longer loses the trust record for hooks it had already approved when
the state table happens to fall inside a managed block. `EvictForeignTablesFromBlock` reports each
relocation as its own warning, mirroring D99's phrasing. The fix lives in `internal/configurator`
next to the existing `codextoml.go` line/header/span primitives it reuses, not in `internal/blocktext`
— that package stays provider-agnostic (it also serves the `AGENTS.md`/`CLAUDE.md` instruction
block).

---

<a id="d127"></a>
## D127 — Codex hook adoption also matches on the decoded command value

**Status: implemented (2026-08-04).**

**Decision.** `codexHookTableOwner` (`internal/provisioning/hooksettings.go`) accepts a
marker-less `[[hooks.<event>]]` registration as hookName's own by **either** identity: D99's
original path-fragment marker (`codexHookOwnershipMarker`), **or** a `command` that decodes to
exactly the command `registerHookConfigTOML` is about to write for that hook. The decode is
`configurator.CodexTableStringValue(body, "command")`, a new exported helper next to
`codextoml.go`'s existing line/key parsing that reads the value of a `key = <string>` assignment
regardless of which of the four TOML string forms it is spelled in — basic `"…"`, literal `'…'`,
multi-line literal `'''…'''`, multi-line basic `"""…"""` — decoding each one's own escaping rules
(or none, for literal forms) rather than comparing raw TOML text. The comparison is byte-exact on
the decoded value, no trimming or normalization.

**Rationale.** D99's path-fragment identity only holds for a hook whose command invokes a script
inside its own materialized directory (`resolveHookCommand` only ever prefixes such a command with
the hook's directory). A hook whose `hook.json` command is a self-contained one-liner — the `jq`
one-liners shipped as `env-block`/`sops-warn` in a real KB — is passed through verbatim and
contains no path fragment at all, so it never matched. After a Codex rewrite dropped the block's
comment markers, `AdoptCodexOrphanTables` could not recognize that class of hook's orphaned
registration; `blocktext.Write` appended the block again, registering — and firing — the hook
twice, the exact failure D99 set out to prevent. The command Cartographer writes for a hook is
otherwise deterministic (`resolveHookCommand` computed once per registration), so its decoded value
is a sound second identity; a new emitted key (e.g. `cartographer_hook = "<name>"`) was rejected —
Codex's config schema is not ours to extend, and an unknown key risks a hard parse failure on the
user's only Codex config file. The decode is necessary, not optional: Codex re-serializes a command
Cartographer wrote as a basic string into a multi-line literal string, and the two spellings share
no useful substring to match on directly.

**Consequences.** `env-block`/`sops-warn`-style hooks stop being duplicated (and firing twice)
after a Codex `config.toml` rewrite. The legacy path-fragment identity is kept, so a registration
written by an older client version is still adopted. A user-authored hook on the same event with a
genuinely different command matches neither identity and is left untouched, as before. **Residual
limit, accepted rather than engineered around:** a hook whose command changes in the narrow window
between a Codex rewrite and the next `connect`/`sync` matches neither identity — the orphan and the
freshly-written registration disagree — and survives as a duplicate; this requires two faults
inside the same window, and the legacy path-fragment match still covers the script-based hooks
where a command change is the most likely of the two. `internal/provisioning/hooksettings.go` (D57,
Claude Code's `settings.json`) has the same shape and the same blind spot, but no duplication was
reproducible there — its JSON path never goes through orphan adoption — so it is left unchanged
pending an actual reproduction.

---

<a id="d137"></a>
## D137 — Declarative provider registry: two tables, owned by the packages that own the concepts

**Decision.** Provider knowledge becomes data in two tables rather than one global registry.
`internal/configurator/registry.go` owns provider **identity and config-file shape**: one
`Descriptor` per provider with its constant and wire value, display name, native MCP config file
and format (JSON with its server key, or a marker-delimited TOML block), whether that file may be
deleted once emptied, whether it can carry MCP auth headers, whether its tool namespace is flat
across servers, its detection evidence (executable names in probe order — a provider may ship its
IDE and CLI surfaces under different ones, as Kiro does with `kiro` and `kiro-cli` — config
directories in probe order, optional macOS app bundle), and its emitter. `internal/provisioning` owns the **kind × provider destination
matrix** plus the `hookMechanisms` table describing how each provider registers a hook natively.
Two orders are exported and both preserved because both are user-visible: `Providers()` (emission
and client iteration) and `DetectionOrder()` (`cartographer agents` and the TUI).

Every matrix cell either names a destination or is marked `unsupported`. A cell **missing** for a
known (kind, provider) pair is a programming error caught by a completeness test, not a silent
degradation to `Unsupported` at apply time — before this, a forgotten switch arm and a deliberate
"this provider cannot do hooks" were the same empty string. An unknown *kind* or *provider*
(a manifest from a newer server) still yields "" and is filtered upstream by `FilterForProvider`;
previously an unknown kind fell through to the skill arm and would have been materialized as one.

**What deliberately stays code.** The four MCP emitters and `translateAgentForProvider`: the output
formats genuinely differ, and data-driving them would only relocate the difference. Two documented
special cases keep naming a provider directly: `configurator.Remove`'s cleanup of the legacy
`.codex/config.json` an old version wrote, and `sync_apply`
(`internal/mcpserver/tools_sync.go`), where the server materializes for Claude Code by
construction — it is not choosing among providers.

**Rationale.** Adding a provider meant editing the same four constants across eight files, and the
matrix documented in `sync.md` existed in the code only as five nested switches whose `default` arm
returned a bare empty string — the shape was invisible to a reader and unenforced on a contributor.
A single global registry was rejected: `provisioning` imports `configurator` and never the reverse,
so putting destination paths in the lower layer would invert the dependency and place provisioning
knowledge in the wrong package. Two tables, each owned where the concept lives, keep the direction
intact. The refactor is behaviour-preserving by construction: the existing suite, including the
connect/disconnect round trip and the configurator goldens, passes unmodified.

## D141 — Hermes is a supported provider that receives deliveries, not installations

`hermes` is a provider in the registry (D137) with exactly one supported kind, `skill`, and four
explicitly unsupported cells. Its skills are **delivered** to
`$HERMES_HOME/skill-inbox/<name>/cartographer/` — the skill's files, provenance-stamped as
everywhere else (D138), plus a generated `SOURCE.md` — and the agent adopts what it chooses through
its own `skill_manage` tool. **Nothing is ever written under `$HERMES_HOME/skills/`.**

**Why delivery and not installation.** That directory belongs to Hermes' own curator, which
archives unused skills, keeps telemetry and honours pins by rewriting what it owns from its
learning loop. Materializing there would put Cartographer and the curator in a permanent fight —
one overwriting from the server, the other rewriting from its own experience — and the loser is the
agent's accumulated learning, which no sync can restore. The convention for *proposing* a skill from
outside already existed (`skill-inbox/`), and it is a curatorial handoff, not an install.

**The four unsupported cells**, each for a stated reason rather than by omission: `agent` — no
native subagent directory; `hook` — no hook mechanism at all, nothing fires at conversation start;
`mcp` — `config.yaml` is rendered by an Ansible role and recreated on the next playbook run;
`instructions` — `SOUL.md`, its always-on slot, is operator-owned and rendered from a template.
Anything Cartographer wrote into the last two would be silently destroyed by the next deploy.
`Unsupported` already means "no approval unblocks it" everywhere in the client (D50), so nothing
else changes. Its trigger is therefore the scheduled timer from D140: with no session hook, the
timer is Hermes' Layer 1.

**No timestamp in the delivery path**, deliberately departing from the
`skill-inbox/<skill>/<timestamp>/` convention. Provisioning must be idempotent: a timestamped
directory per sync would accumulate a fresh copy every timer tick and no prune could tell stale from
current. One stable directory per (skill, source) is updated in place — the last path segment names
the proposer, so another source never collides — and the history of the proposal lives in the KB's
git log, which is where it belongs. `SOURCE.md` carries the artifact's content hash, so the agent
distinguishes an unchanged re-delivery from a new proposal. `SOURCE.md` is generated, so a KB skill
shipping its own is a collision: that one artifact fails with a warning naming it and is not
recorded in the lockfile, while the rest of the sync completes.

**Per-provider base directories.** Every other provider materializes under one shared base dir;
Hermes' root is elsewhere on the machine (`/opt/data` in the reference deployment), so `Lock` gains
an optional `base_dir` recording the directory its `ManagedFile` paths are relative to. Empty — every
lockfile written before this, and every other provider — means "the lockfile's own directory", so
no migration runs and nothing changes meaning. Prune and on-disk verification (D139) read it through
`LockBaseDir`; the lockfile itself stays a **single v2 file** in the client's target directory with
one `Lock` per provider. `$HERMES_HOME` unset makes `connect hermes` fail naming the variable,
before anything is written: falling back to the home directory would scatter files where the agent
never looks.

**Connect says what it does not do.** Hermes has no MCP emitter, so `connect hermes` writes no MCP
entry and reports that its endpoint stays the operator's job — silently doing nothing there would
read as a bug. For the same reason it is absent from the interactive connect form, which offers the
providers whose MCP configuration `connect` actually writes; Hermes is connected from the CLI, where
the missing-`$HERMES_HOME` failure can be stated plainly.

## D142 — `reconnect`: rebuild a client configuration, never automatically

`cartographer reconnect [provider|all]` is a full `disconnect` followed by a full `connect` for the
selected providers, in one invocation. It **reuses `doDisconnect` and `doConnect`** rather than
being a third implementation of either: the two halves already encode every rule about what may be
removed and what must be written, and a parallel implementation would drift from them silently.

**Why a rebuild is needed at all.** `sync` does more than fetch artifacts — it re-enumerates the
mounted KBs, rewrites the MCP entries, re-ensures the bootstrap hook and materializes — and covers
almost every kind of drift. What it structurally cannot cover is what an **older Cartographer
version** left behind: pruning is managed-only, so a generated plugin whose filename changed, a
managed block whose marker spelling changed, or a hook registered outside the block is not in the
current `managed[]` and survives every sync. The code already carries scars from exactly this —
`instructionsBlockBeginPrefix` exists solely to recognize blocks written by older versions in
another language, and D99's repair removes Codex hook registrations left outside the managed block
so a hook does not fire twice. The connect half writes the current shape from nothing, which is what
removes them.

**It is a rebuild, not a reset.** Every setting is read from `.cartographer.yaml` and re-applied:
server URL and name, auth mode, token env, trust, pinned signing keys, MCP approvals, search roots
and paths. That falls out of two existing rules rather than new code — `disconnect` never deletes
the file (D64), and `connect` reads it back — but it is the load-bearing property: a user who has to
re-approve every MCP descriptor after an upgrade will simply stop running the command.

**It is never automatic.** No upgrade path invokes it. The reasoning is D121's — automatic repair
never invents an approval and never broadens trust — plus the observation that removing and
rewriting provider configuration is a bigger hammer than repairing it in place. `upgrade-repair`
stays the automatic path and keeps calling the ordinary sync.

**Partial-failure contract.** If the connect half fails after the disconnect half succeeded, the
command exits 2, names the providers now left without a configuration, and prints the exact
`cartographer connect` invocation — same settings — that finishes the job. Silence there would leave
an agent without its MCP endpoint and no way to know it; a dry run leaves nothing disconnected and
therefore says nothing.

**The lockfile records the server version.** Each provider's `Lock` gains `server_version`
(optional, `omitempty`), taken from the `/health` snapshot `sync` already fetches — no second
request. Empty means "unknown" (every lockfile written before this, and any sync that could not
reach the server) and never triggers a report: the first sync after an upgrade must not tell every
user something it cannot know. An unreachable server leaves the recorded value **unchanged** rather
than blanking it, and a `dev` version on either side is ignored, the same rule the advisory
client/server skew line already uses. On a real difference `sync` prints exactly one line — both
versions and the `reconnect` recommendation — once per invocation regardless of provider count, then
proceeds with the ordinary sync. It reports; it does not escalate. `status` shows the same fact
without running a sync. The version is recorded **per provider** because providers are synced
independently.

## D143 — `doctor`: a separate command that diagnoses and never repairs

`cartographer doctor` is a new read-only command that runs eight checks over this machine's client
configuration and reports what is left over or missing, each finding naming a real path and the
command that fixes it.

**Why not `status --strict`.** These checks read every provider's native config file, enumerate its
MCP entries, count marker pairs in instruction files and scan Codex's `config.toml` for orphaned
tables. That is an order of magnitude more work than `status`' revision comparison — and `status` is
on the path the bootstrap hook's success message prints. Conflating them would slow down the fast
command to serve the rare one. Two commands, two costs.

**Diagnosis only.** No `--fix` flag, and no writing of any kind: not even the lockfile migration
`ReadLockFile` performs in memory anyway, not a created directory, not a refreshed cache. The repair
paths already exist — `sync` restores managed files and removes Codex's double registrations,
`reconnect` (D142) rebuilds, `service sync-timer install` adds the trigger — and each is individually
reviewable. A doctor that silently fixes things is a doctor nobody can predict, and the operator
would lose the one thing this command is for: knowing what was wrong.

**Severity model.** `error` means something is broken now (a managed file missing, a hook firing
twice, an MCP entry pointing at a KB that is gone); `warning` means stale or suboptimal (a v1
lockfile, no trigger for a hook-less provider, a version difference). Both exit 1 — the operator
should look at either — but the severity orders the output so the actionable one is read first. A
third, `info`, carries what cannot be acted on by itself and never changes the exit code: managed
entries recorded before materialized hashes existed cannot be verified at all, and reporting that
per artifact would bury the real findings. Exit codes stay `status`' convention (0 clean, 1 findings,
2 command error) so scripts treat them identically.

**Every finding names a path.** "Something is wrong with claude" is not a diagnosis. The path is
absolute and may legitimately be gone — that is exactly what a `missing` finding says — but it is
always a place the operator can go and look.

**Useful offline.** The only network access is the `/health` probe `sync` already makes, and failing
it produces one `warning` finding rather than aborting: a client whose server is down is precisely
when someone runs `doctor`.

**Never on the session path.** Neither the bootstrap hook nor the scheduled timer invokes it. Eight
checks on every session start is the background cost D60 avoided by keeping `bootstrap.sh` silent,
deterministic and always `exit 0`.

**One byproduct.** The bootstrap hook's lockfile entries carried no materialized hash, so D139's
verification could not check them and `doctor` would have reported "unverifiable" on every healthy
machine forever. `EnsureBootstrapHook` now records the hash of the two files it writes — computed
from the same constants, never read back — which makes the client-generated hook verifiable like any
other managed artifact.

## D146 — Existence and content are verified with different evidence

D139 gated its whole on-disk check behind a recorded `materialized_hash`: an entry without one
returned `unknown` before the filesystem was ever touched. `unknown` is not healable by design, and
`status` drops non-healable findings, so a managed artifact **deleted from disk** kept reporting
`in-sync` on any client whose lockfile predated D138 — the exact failure D139 was written to catch,
surviving in the population most likely to hit it.

The gate conflated two questions that need different evidence. *Did the bytes change?* cannot be
answered without a recorded hash. *Is the path still there?* can, by `stat` alone. `verifyArtifact`
now resolves the destination once (`managedDest`), stats it, and only then falls through to the hash
gate: a vanished artifact is `missing` regardless of what the lockfile knows about its content,
while one that is present but hashless stays `unknown`.

**The first-upgrade guarantee is untouched**, which is what made the original gate defensible: no
file that exists is ever rewritten on account of a missing hash. Restoring something that is *gone*
is not the mass rewrite that argument protects against — it writes only where there is nothing, and
it is what the operator already believes `sync` does.

Rejected: healing `unknown` outright (reintroduces the mass rewrite), and special-casing `status` to
surface unknowns as drift (would make every pre-D138 client exit 1 until it reconnects, and still
leave `sync` unable to restore the missing file). `doctor` keeps aggregating the remaining unknowns
into its one advisory line — with existence now covered, that line means what it says: content that
cannot be compared, not artifacts that might be absent.

## D147 — Every reported write is observed, never intended

Two lines claimed work that had not happened. `connect --dry-run` printed `[kiro] wrote
.kiro/skills/…/SKILL.md` and `connected: kiro` for files it did not write, because `Apply` fills
`AppliedResult.Written` with what it *would* write and `printApplySummary` never received the flag —
while `printConnectResult`, one frame above, branched on it correctly for its own two loops. And
`connect hermes` printed `wrote MCP entry cartographer` immediately before the warning explaining
that Hermes' MCP configuration is never written by Cartographer (D141), because the line was
rendered from `entryNames(entries)` — the entries built from the client config, independent of which
providers can accept them.

Neither was a functional defect: the files on disk were right in both cases. The cost is
epistemic. `connect` is the command whose output *is* its result — run once, in an unfamiliar setup,
with nothing to compare against — and `--dry-run` exists precisely to be read before committing. A
line saying `wrote` that corresponds to no write is worth less than no line at all, and in the
Hermes case it argued against the warning printed to correct it, arriving first.

**The rule.** A `wrote`/`pruned`/`restored`/`connected` line is emitted from the code path that
observed the effect, never from the one that planned it. Mechanically: `printApplySummary` takes
`dryRun` and renders the conditional (`would write`), suppressing the hook-registration line, which
reports a side effect on a shared file that a dry run does not perform either; `sync --dry-run` ends
with `would sync to revision`; and both the MCP-entry lines and the restart hint are scoped by
`providersManagingMCP`, so a mixed `connect claude,hermes` names only claude in the hint.

Rejected: dropping the MCP-entry line whenever `ConfigsWritten` is empty. It reads as equivalent but
couples an MCP claim to an unrelated emptiness — a provider that writes its entry into a file
already containing it would lose the line for the wrong reason. The predicate is "does this provider
have an emitter", so that is what the code asks.
