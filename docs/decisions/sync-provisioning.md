# Synchronization and provisioning decisions

Manifest/lockfile synchronization and provider-neutral artifact provisioning. Current behavior: [`../sync.md`](../sync.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d27"></a>
## D27 — Client synchronization: manifest+revision, lockfile, layered triggers
**Decision.** Client realignment (skill/hook/agent) is based on three objects: a
server-side **provisioning manifest** (`revision` = aggregate hash), a client-side
**lockfile** (applied state + `managed[]`), and a `ProvisionedArtifact` abstraction with an
extensible `kind`. Layered triggers (`SessionStart` hook, `sync_check`/`sync_apply` tools,
optional MCP push), default **notify + signature gate**, **managed-only prune**.
**Rationale.** A pull-based model with fingerprint is O(1) and cross-provider on a
heterogeneous request/response transport; the `kind` abstraction avoids redesigning for hooks/subagents.
Skills are executable code: the signature gate aligns sync with the supply-chain invariants.
*(Superseded state: Layer 3 and codex/kiro providers → D34; `cartographer-configure` removed in
favor of `cartographer connect/status/sync` → D37/D40.)*
Details: `docs/sync.md`.

---

<a id="d34"></a>
## D34 — Synchronization: Layer 3 push over stdio + codex/kiro materialization

**Decision.** Two pieces of the sync model (D27) completed:
- **codex/kiro providers**: `skillDestDir` (`internal/provisioning`) now also maps `codex` → `.codex/skills/<nome>/` and `kiro` → `.kiro/skills/<nome>/` — they move out of `needs_approval` to direct materialization like claude/opencode. Everything else (manifest, lockfile, diff, prune) was already provider-generic.
- **Layer 3 (push)**: `Server.Notify(method, params)` emits a JSON-RPC notification on the shared stdio encoder (serialized by `writeMu`); the `notifyWrap` helper wraps `skill_install` (outside `gitWrap`, fires after the commit, only on success) and emits `notifications/skills/list_changed`. Capability `skills.listChanged: true` announced in `initialize`.

**Rationale.** On the hand-rolled HTTP transport (D16: request/response, no SSE) the server has no channel for unsolicited pushes → `Server.Notify` is a **no-op** when not inside `Run` (`enc == nil`), and Layer 3 applies only to **stdio**, degrading gracefully to Layers 1–2 over HTTP (consistent with the "additive" design of `sync.md`). The crucial choice is the **lock discipline**: `writeMu` protects each individual `Encode` but is never held during `dispatch` (where `Notify` is called, same goroutine) → no nested locks and no deadlock; verified with `go test -race`. The practical value over stdio is limited (the agent calling `skill_install` is also the consumer), but the `Notify` infrastructure is the hook-in point for future emitters (server-side git pull, bundle hot-reload) with no further changes to the loop.

---

<a id="d40"></a>
## D40 — `sync_pull` and client-side trust; anti path-traversal guard in `provisioning.Apply`

**Decision.** New MCP tool `sync_pull()` (read-only): returns the manifest with each artifact's
contents embedded in base64, so a remote HTTP client without a shared filesystem
can materialize locally. Unlike `sync_apply`, `sync_pull` does not accept
`auto_trust`: the trust decision is entirely client-side. Multi-provider lockfile v2
(`LockFile{Providers: map[string]Lock}`, automatic migration from v1). `provisioning.Apply`
gains an explicit guard: artifact name and file path must be `filepath.IsLocal`.
**Rationale.** The server must not decide trust on behalf of the remote client, which may have
a different policy — hence `sync_apply` (local, trust = tool parameter) vs `sync_pull`
(remote, trust decided by whoever materializes). The path-traversal guard is necessary because with
`sync_pull` the paths arrive as network data (JSON), not from an already trusted filesystem.
Details: `docs/sync.md`, `docs/control-plane.md` §Client synchronization ↔ provisioning.

---

<a id="d48"></a>
## D48 — Provisioning extended to `kind: agent`/`hook`; hooks without auto-merge into `settings.json`; per-kind counts

**Decision.** Extends `internal/provisioning` (already kind-generic on Manifest/Lock/Diff, D27/D34/
D40) to two new kinds. `kb.Init` creates `agents/`/`hooks/` (optional/backward-compat); `BuildManifest`
scans `agents/<nome>.md` (one file = one agent artifact) and `hooks/<nome>/` (one directory = one
hook artifact). `destDir(kind, name, provider)` generalizes `skillDestDir`: for agent/hook only
`claude` has a known destination (`.claude/agents/<nome>.md` single file, `.claude/hooks/<nome>/`
directory) — other providers remain `needs_approval`. **Hooks with no automatic merge into
`settings.json`**: that file is user-owned with nested arrays and no safe merge/prune —
Cartographer materializes only the folder and prints a manual instruction (**superseded by D57**:
registration is now automatic). New `KindCounts` aggregates installed/total per kind, shown by
`status`/TUI.
**Rationale.** The model was already kind-generic by design (D27/D34): the extension only required
new emitters and a destination branch. The non-merge of hooks follows the same least-surprise
philosophy as prune (D40): a user file with nested structure is not a safe target for
silent automatic writes. The single file for agents mirrors the native format of a
Claude subagent, instead of forcing them into the skills' directory convention.
Details: `docs/sync.md` §Agents and hooks, `docs/interoperability.md` §Provider capability matrix.

---

<a id="d50"></a>
## D50 — Honest per-provider sync state: `unsupported` ≠ `needs_approval`, `InSync` requires zero differences

**Context.** After D48, an OpenCode sync showed "in-sync" and at the same time "8 needs approval": unsupported agents/
hooks ended up in `NeedsApproval` (but `--auto-trust` would never have unblocked them) and
`Diff.InSync` ignored artifacts left out.

**Decision.** `AppliedResult.Unsupported` (new field): `destRel == ""` → `Unsupported`, no longer
`NeedsApproval`. `provisioning.FilterForProvider` filters the manifest per provider before
Apply/diff/counts. `Diff.InSync` now requires identical revision **and** zero Added/Updated/
Removed.

**Discarded alternatives.** A dedicated badge for unsupported kinds in the counts; marking the
revision as not applied when NeedsApproval remain (it would have made every sync re-attempt
the apply with no progress).
Details: `docs/sync.md` §Agents and hooks.

---

<a id="d54"></a>
## D54 — Per-server trust persisted at `connect` (replaces the recurring `--auto-trust` gate)

**Context.** Artifacts from the KB arrive `signed:false` (D40): without `--auto-trust`, every
`sync`/`connect` leaves them in `needs_approval`. The TUI did not expose `--auto-trust` at all, so
from there they stayed pending forever — in a single-user context, "re-approving" at every sync is pure
friction that verifies nothing.

**Decision.** Trust becomes an explicit choice made once at `connect`, persisted:
`clientconfig.Config.Trust bool` (default `true`), toggle in the connect form shared CLI/TUI.
`sync`/`status`/TUI use `cfg.Trust` as default; `--auto-trust` remains a one-off override
(`cfg.Trust || --auto-trust`) at materialization time. `status`/TUI never mutate `Signed`: they
report a separate `trust` state per artifact (`snapshotArtifacts`, D115) that reads `cfg.Trust`
read-only to report `trusted` instead of `needs_approval`, so as not to show a false pending
approval without pretending the artifact was cryptographically verified. No symmetric
`--no-trust`: revoking is rare, manually setting `trust: false` in `.cartographer.yaml` is enough.
**Rationale.** The signature placeholder (D40) stays identical server-side; this decision concerns
only who/when decides to trust (the user, at connect, persisted — not relitigated at every sync).
**Discarded alternatives.** A symmetric CLI `--no-trust`: no real use case, it would only have
doubled the flag surface to maintain.
Details: `docs/configurator.md` §`.cartographer.yaml`, §`cartographer connect`.

---

<a id="d55"></a>
## D55 — Agents also materialized on OpenCode, via frontmatter translation

**Context.** D48 materializes `kind: agent` only on `claude` — but OpenCode has its own native
subagents (`.opencode/agent/<nome>.md`, singular dir, incompatible frontmatter: `description` +
`mode: subagent`, name from the filename).

**Decision.** `destDir` maps `agent`×`opencode` → `.opencode/agent/<nome>.md` (codex/kiro
remain `unsupported`). The content is not copied verbatim: `translateAgentForProvider` (a pure
function, called by `Apply` before writing) extracts `description` from the source Claude
frontmatter and emits a minimal OpenCode frontmatter + verbatim body. Claude-only fields that
cannot be mapped reliably (`tools`, `model`, `name`) are **dropped**, not guessed.
`ContentHash` remains that of the source (unchanged): translation happens only at write time,
never in the hash computation — drift detection stays agnostic to the destination provider.
**Rationale.** The translation lives in `Apply` (client-side), not in the manifest, the same
architectural choice as D48. Dropping unmappable fields avoids silently wrong behavior
on an artifact that drives an agent's behavior.
**Discarded alternatives.** Mapping `tools`/`model` with a best-effort heuristic: syntax and names
diverge enough to make any automatic mapping fragile and silently incorrect.
Details: `docs/sync.md` §Agents and hooks, `docs/interoperability.md` §Provider capability matrix.

---

<a id="d56"></a>
## D56 — `instructions` kind: KB imprinting via managed block in the global instruction files

**Context.** LLM agents connected via MCP have no way to discover that a KB exists: an
"imprinting" is needed in the global instructions file that every provider always reads at each session
(`CLAUDE.md`, `AGENTS.md`, Kiro's steering).

**Decision.** New `instructions` kind, one per KB. Unlike skill/agent/hook, the
content does not live on disk: it is generated by `generateKBInstructions(kbName, kbRoot)`, a pure,
deterministic function that lists the KB's top-level archives and gives operational instructions
(`search`/`kb_overview`/`concept_read`/`concept_write`/`log_append`); `ContentHash` is the sha256 of the
generated content. Materialization as a **marker-delimited block**
(`<!-- cartographer:instructions:begin/end -->`) inside the user's file — never a dedicated
file (except Kiro, `.kiro/steering/cartographer.md`, which has no pre-existing generic
file) — so everything outside the markers is never touched. Managed as a
**group** (`applyInstructionsGroup`): a single file per provider contains the concatenation of
all current KB instructions, rebuilt in full when anything in the kind changes. Non-destructive
prune: removes only the block, deletes the file only if it ends up empty. Signature gate
unchanged: unsigned `instructions` artifacts stay in `NeedsApproval` like every other kind.
**Rationale.** The "never touch unmanaged content" rule must be applied *inside* a file, not at
the whole-file level, because the destination is by construction a file that may already belong to
the user. The marker block guarantees idempotence and non-destructiveness without semantic merge.
**Discarded alternatives.** A dedicated file for all providers (it would not be read automatically
unless already referenced by the main file); a runtime hook injecting the context (requires
session-hook support, not universal, and starts from scratch every time).
Details: `docs/sync.md` §Instructions.

---

<a id="d57"></a>
## D57 — Claude Code hooks: automatic registration in `settings.json`

**Context.** D48 materialized a hook (`hooks/<nome>/` → `.claude/hooks/<nome>/`) but stopped
there: `settings.json` was never touched, for fear of duplicating entries in
`hooks.<Event>[]` at every sync (nested arrays, no ownership marker) and of not being able to
prune safely — a materialized but dead hook, to be registered by hand.

**Decision.** For the `claude` provider only, `Apply` now automatically registers/updates the
entry in `<targetDir>/.claude/settings.json` right after materializing the hook's files
(`internal/provisioning/hooksettings.go`). Ownership criterion: an entry is "Cartographer's for
hook `<nome>`" if and only if its `command` contains the substring `.claude/hooks/<nome>/`
(`hookOwnershipMarker`) — the materialized path *is* the signature, no extra field to invent.
`upsertHookEntry` removes every entry with that marker and inserts a fresh one → idempotent. The
file is decoded into a `map[string]interface{}`, not a fixed struct: unknown keys
survive, only the key order is not preserved (acceptable: the invariant is "no user data
lost"). Prune removes the entry via the same marker when the hook disappears.
**Rationale.** A generic JSON deep-merge has no "obvious" criterion for an array without an
identifying key; the per-path marker is targeted and does not require extending the entry format (discarded: a
dedicated `_cartographer` field, risk of breakage with future Claude Code schema checks).
Details: `docs/sync.md` §Agents and hooks.

**Update (bare-command bug).** `resolveHookCommand` joined *any* non-absolute first token
(and non-`$VAR`) to the hook's dir — correct for `./notify.sh`, broken for a shell one-liner
like `jq -e ...`: it produced `.claude/hooks/<nome>/jq`, a nonexistent binary, and the command's
`|| true` masked the failure at every invocation. Now the join happens only if the token
contains `/` (relative path); bare names stay verbatim, resolved via PATH as in a shell.
Since the ownership marker *is* the path in the command, a bare command would never contain it
(entry neither idempotent nor prunable): for the claude provider only, `registerHookSettings` appends
the marker as an inert shell comment (`# cartographer-hook: .claude/hooks/<nome>/`) —
syntactically neutral, preserves D57's substring criterion without extra fields. Codex/OpenCode
do not need it (per-block/per-file ownership).

---

<a id="d58"></a>
## D58 — Real Codex CLI integration: managed-block `config.toml`, TOML agents, hook engine

**The bug.** Since D23, `emitCodex` wrote `.codex/config.json` Claude Code-style — but Codex CLI
never reads that file: the only configuration it consults is `~/.codex/config.toml`, section
`[mcp_servers.<id>]`. The MCP integration with Codex never worked in practice.

**Decision.** Three extensions, verified against the official Codex docs:
- **MCP → managed-block `config.toml`.** `config.toml` is hand-curated (comments, order): never
  parsed/reserialized as generic TOML. `emitCodex` generates only the body of the
  `[mcp_servers.cartographer]` block; `Apply`/`Remove` materialize it with the new generic
  `internal/blocktext` package (`Write`/`Remove`/`ReplaceBetween` on text markers) inside dedicated
  markers. `Remove` also cleans up any legacy pre-D58 `.codex/config.json`.
- **Agent → `.codex/agents/<nome>.toml`.** `destDir` maps `agent`×`codex` to this path;
  `translateAgentForCodex` (parallel to D55) extracts `description` and puts the body in
  `developer_instructions`. Fields with no reliable equivalent (`tools`, `model`) omitted, same
  policy as D55: never guess a value.
- **Hook → materialization + hooks engine.** `destDir` maps `hook`×`codex` to
  `.codex/hooks/<nome>/`; registration goes inside the managed block of `config.toml` as an array
  of TOML tables, with a per-hook marker (`# cartographer:hook:<nome>:begin/end`) — same
  ownership-per-block principle as D57, here per-marker in text instead of per-substring in JSON.
**Rationale.** `internal/blocktext` factors out a problem that now recurs twice in
`config.toml` (MCP entry + N hooks); the pre-existing version for `instructions` (D56, HTML markers)
remains unrefactored — stable code, no reason to touch it alongside an unrelated
change.
**Open question.** The `hook.json` `matcher` is written for Claude Code (free substring);
Codex interprets it as a regex — no automatic conversion planned.
Details: `docs/sync.md` §Agents and hooks.
**Follow-up (July 2026).** The TUI dashboard (`mcpConfigStatus` in `cmd/cartographer/tui.go`)
checked for the MCP entry's presence with a single `json.Unmarshal`, still calibrated on the old
`config.json`: for Codex the file is TOML, so the parse always failed and the `mcp-config` badge
showed `missing` even with a correct config. Now, for providers with a `FilePath` ending in
`.toml`, the check looks for the `[mcp_servers.<name>]` table instead of parsing JSON.

---

<a id="d59"></a>
## D59 — OpenCode hooks: generated JS plugin

**Context.** After D57/D58, `kind: hook` remained `Unsupported` on OpenCode: no declarative
hooks to register in a file — OpenCode instead loads JS/TS plugins from
`~/.config/opencode/plugins/` (auto-loaded, no entry in `opencode.json`).

**Decision.** `destDir("hook", _, opencode)` materializes the files in `.opencode/hooks/<nome>/`;
`Apply` generates — if the KB event is mappable (`PreToolUse`→`tool.execute.before`,
`PostToolUse`→`tool.execute.after`, `SessionStart`/`Stop` on the generic pub/sub bus filtered by
`event.type`, other events → no plugin) — an entire deterministic plugin file
`cartographer-<nome>.js` that runs the materialized script. **Per-file ownership, not
per-block**: unlike D57/D58, the generated file belongs entirely to Cartographer — rewritten
in full on update, deleted on prune, no block parsing. Unmappable event →
`AppliedResult.Warnings`, not an error. The matcher (only `PreToolUse`/`PostToolUse`) uses a
bidirectional case-insensitive substring comparison (a heuristic, not a guaranteed tool-name table).
**Rationale.** One file per hook is consistent with the existing skill/agent scheme and makes prune
trivial (`os.Remove`), discarding a single "router" plugin that would be harder to maintain incrementally.
**Open question.** The substring matcher is a heuristic: a hook with a very specific matcher
might not behave identically on the two providers.
Details: `docs/sync.md` §Agents and hooks.

---

<a id="d60"></a>
## D60 — Client-side bootstrap hook: auto-sync at session start (WP4)

**Context.** `docs/sync.md` §Layer 1 described, since before D57, a `SessionStart` hook that
invokes `cartographer sync`/`status` at session start — never implemented: D57/D58/D59 built
the registration mechanism for *KB* hooks, but this one never comes from a KB. An
artifact generated entirely by the client was needed.

**Decision.** `internal/provisioning/bootstrap.go` introduces `EnsureBootstrapHook`: it materializes
a deterministic script (`bootstrap.sh`, calls `cartographer sync --auto-trust`) and reuses
verbatim `registerHookSettings`/`registerHookConfigTOML`/`registerOpenCodePlugin` (D57/D58/D59) for
native registration — zero new code. Reserved name `cartographer-bootstrap`. Called by
`doConnect`/`cmdSync` before the manifest fetch, independent of server reachability.
**"Orphan" protection**: the server manifest will never contain this artifact — `ComputeDiff`
explicitly excludes `Kind=="hook" && Name==BootstrapHookName` from the `Removed` computation, so the
bootstrap is not deleted and recreated at every sync (same principle already used for `kind:
instructions`). Name collision with a same-named KB hook → ignored with a warning, never overwritten.
`kiro` (no native hook mechanism) remains a no-op, degraded to Layer 2.
**Rationale.** Protecting the bootstrap from the diff (instead of having it reappear at every sync) avoids
noise (files/config rewritten every round) and a window in which the hook does not exist between prune and
rematerialization.
Details: `docs/sync.md` §Layer 1.

---

<a id="d61"></a>
## D61 — Instructions: auto-generated agents section + curated `instructions.md` (WP5)

**Context.** D56 generates the `instructions` block with archives only + generic operational
instructions. It says nothing about the **agents provisioned by the same KB** (D48/D55): the main
agent does not know local subagents exist nor when to delegate. There is also no place where
the operator can write domain-specific orchestration directives.

**Decision.** `generateKBInstructions` (signature and materialization mechanism unchanged)
adds two optional trailing sections, each omitted if empty: (1) an auto-generated agents
section (name + `description` from the frontmatter of `agents/<nome>.md`, sorted by name); (2)
an optional curated file `<kbRoot>/instructions.md`, free text included verbatim. The hash remains
`sha256(generateKBInstructions(...))`: no change to `BuildManifest`, drift is still detected
because the function now also reads these files. `instructions.md` is not a concept: it lives
outside `data/`, never indexed nor materialized as a standalone file.
**Discarded alternatives.** `instructions.md` as an additional skill/agent (it would require a new
kind and its own destination, instead of enriching the already injected block); keeping it outside the
KB in server config (it would lose the git versioning shared with the rest of the KB).
Details: `docs/sync.md` §Instructions.

---

<a id="d69"></a>
## D69 — `kind: mcp` provisioning: third-party MCP servers distributed by the KBs

**Status: active.** HTTP transport only (per the starting decision). WP1–WP4 and WP6
implemented as planned; WP5 (trust) implemented with a more restrictive security choice
than the plan implied (see below); the server-side allow-list
(optional in the plan) **not implemented**, deferred to Phase 3.

**Context.** KBs already distribute skills, agents, hooks, and instructions to clients via the
manifest→lockfile→apply flow (D27/D48/D56). A kind for **third-party MCP servers** is missing: today
the only MCP the client configures in the agents is Cartographer itself (`internal/configurator`,
`cartographer:mcp:*` blocks). All the necessary infrastructure already exists: the configurator can
emit MCP config for the 4 providers (for itself), `hooksettings.go` has the idempotent merge
+ prune patterns on the providers' config files. The feature is "generalize MCP emission and hook it
into the artifact flow". N.B.: the KB `mcp/` folder introduced here has **no** relation to the
`mcp/` removed by D28 (that was something else, June 2026).

**Starting decision.** HTTP transport only in this iteration. `stdio` implies referencing a
command/binary present on the client — more useful but thornier (distribution, paths, security);
it will be added later (the `type` field is already in the schema for that day).

**WP1 — Source format in the KB.** `mcp/` folder in the KB, one JSON file per server:
`mcp/<nome>.json` (single-file like agents, not a directory like skills). Provider-neutral
schema: `{"type": "http", "url", "headers", "env"}`. **Security constraint**: no
secrets in the file — the values of `headers`/`env` support only `${VAR}` references resolved
from the client's environment (`token_env` pattern, D64); `parseMCPServerSpec` rejects a value with
no `${VAR}` reference at all (it looks like a literal secret), a `type` other than `"http"`, or a
missing `url`. `env` is validated with the same rule for future `stdio` compatibility, but it is
not yet emitted by any provider (see WP3): none of the 4 currently exposes a verified channel for
generic env vars on an "http" server — only the Authorization header can be represented
reliably.

**WP2 — BuildManifest.** New step in `BuildManifest` (provisioning.go): scan of `mcp/*.json`
for each KB → an `Artifact{Kind: "mcp", Source: "kb:<nome>"}` with `contentHashFile` (like
agents). Missing folder → zero artifacts (backward compat). Schema parse+validation happens here, so
a malformed file or one with a literal secret fails the build, not the apply.

**WP3 — Apply per provider.** Unlike skills/hooks, an MCP server does not materialize its own
files: it **merges into the provider's native config** (`internal/provisioning/mcpsettings.go`,
`registerMCPServer`/`removeMCPServer`):
- claude: key `mcpServers.<nome>` in `~/.claude.json`;
- codex: block `[mcp_servers.<nome>]` in `.codex/config.toml` with markers
  `# cartographer:mcp:<nome>:begin/end` (pattern from `registerHookConfigTOML`, distinct from the
  unnamed `cartographer:mcp:begin/end` block that `internal/configurator` writes for the
  Cartographer entry itself via `connect` — no collision);
- opencode: key `mcp.<nome>` in `opencode.json`;
- kiro: `mcpServers.<nome>` in `.kiro/settings/mcp.json`.

Key refactor: `internal/configurator.EmitServer(name, spec ServerSpec, provider)` extracted from
`Emit`/`ServerConfig` (which is now a thin wrapper over `EmitServer(cfg.Name, cfg.toSpec(),
provider)`), used both by `connect` (for the Cartographer entry) and by `provisioning.Apply` (for the
KBs' servers). Each `${VAR}` reference is translated per provider: claude/kiro/codex leave it
verbatim, OpenCode translates it to `{env:VAR}`. Native limits not worked around with heuristics: Codex
exposes only `bearer_token_env_var` (an `Authorization: Bearer ${VAR}` header translates to it, every
other header is dropped with a warning in `EmitResult.Warnings`); Kiro never had a header
field for MCP servers (pre-existing limit, not introduced here) — a KB server with headers
generates a warning in `AppliedResult.Warnings`, not an error. Invariant preserved: only
own keys/blocks are managed, never the rest of the file (existing `connect` goldens unchanged).

**WP4 — Prune and disconnect.** `ManagedFile{Kind: "mcp"}` in the lock for each written server;
`PruneManaged` removes the single key/block (`removeMCPServer`, analogous to
`removeHookEntries`/`removeHookConfigTOML`), never the whole file — with the same empty-shell
cleanup as `configurator.Remove` (D63) for kiro/opencode. Round-trip test in
`provisioning_disconnect_test.go` extended: connect with a KB carrying an MCP server → disconnect →
clean provider configs (`TestRoundTrip_ConnectDisconnect_NessunResiduo`).

**WP5 — Trust and remote sync.** An MCP server is an endpoint that receives the agent's data:
Before D114/D115, `BuildManifest` marked the `mcp` kind **always** `Signed:false`, regardless of `autoTrust` — unlike
skill/agent/hook/instructions, which `autoTrust` signs. The remote client likewise excluded `mcp`
from its generic upgrade via `cfg.Trust`/`--auto-trust`: a stricter policy than the other kinds,
`NeedsApproval` at first appearance and at every hash change, even with `AutoTrust` active.
**Deviation from the plan, since resolved**: this iteration had no mechanism to mark a single
`mcp` artifact as approved — the only generic gate was `cfg.Trust`/`--auto-trust`, which `mcp`
ignores by construction. The persisted point approval (of what exactly, for which hash) arrived
with D115 as `cfg.MCPApprovals`; the client-side upgrade hook it describes no longer exists,
since D114 made `Signed` an exclusively cryptographic result. `sync_pull`/`tools_sync.go` and the HTTP client do not filter by kind (verified):
an `mcp` artifact travels as a single `ArtifactFile`, same schema as an agent.

**Server-side allow-list**: **not implemented** (optional in the plan, tied to the Phase 3
"MCP registry allow-list" item below).

**WP6 — Documentation and closure.** `docs/sync.md` §MCP servers, `docs/configurator.md`, this
entry and the release tracking state then in use. Tests:
`internal/provisioning/mcpspec_test.go` (schema validation),
`internal/configurator/configurator_mcpserver_test.go` (`EmitServer` goldens for the 4 providers),
`internal/provisioning/provisioning_mcp_test.go` (BuildManifest/Apply/Prune), extended round-trip in
`provisioning_disconnect_test.go`.

---

<a id="d75"></a>
## D75 — Path portability across machines: placeholders auto-resolved via git remote

**Status: implemented (2026-07-10); the "never absolute paths" phrasing below is corrected by
[D124](data-plane.md#d124)** — read "client-local absolute paths"; a Map-level allowlist now
distinguishes those from operational target paths that are identical on every reader's machine.
WP1–WP6 complete: `internal/repoindex` (Scan/Resolve, cache
`~/.config/cartographer/repos.json`), `search_roots`/`paths` in `.cartographer.yaml`, client-side
expansion in `provisioning.Apply` with hash on the expanded content, "Local paths" table in the
instructions block, `cartographer resolve`, `machine_path` lint. Details → `docs/sync.md`
§Path portability placeholders, `docs/configurator.md` (`resolve`, `.cartographer.yaml`).

**Deviations/clarifications versus the original spec:**

- `ApplyOptions` gains an explicit `ExpandPlaceholders bool` field (besides `SearchRoots`/
  `Paths`), not mentioned in the spec: it makes "the server never expands anything" a structural
  guarantee (the field stays `false` by construction in `internal/mcpserver`, never set there)
  instead of depending on the accidental absence of `SearchRoots`/`Paths` — it also prevents a
  server-side `Resolve` from attempting a `LoadCache`/`Scan` on the server's filesystem anyway.
- Intentional consequence of hash-on-expanded-content: for an artifact containing at least one
  placeholder, `ManagedFile.ContentHash` (client, expanded) never converges with `Artifact.ContentHash`
  (server, raw) — that artifact is therefore re-materialized at **every** `sync`, not only when
  the KB content really changes. It is not a bug: it guarantees that a repo re-cloned elsewhere (or an
  updated `search_roots`/`paths`) is reflected in the expanded path at the next sync, instead of
  staying frozen in the lockfile. No impact on artifacts without placeholders (zero drift,
  tested).

**Decision.** In shared content (concepts and provisioning artifacts) **never absolute paths**. Two placeholders:

- `{{repo:<name>}}` (short form) / `{{repo:<host>/<owner>/<name>}}` (full, canonical form) — resolved **automatically**: the key is the **normalized git remote**, identical on every team machine; the local path is discovered by scanning the filesystem. Zero manual mapping for the common case.
- `{{path:<nome>}}` — manual `paths:` mapping in `.cartographer.yaml`, fallback for directories that are not git repos.

No server-side substitution: it would break `content_hash`/`if_match` (hash on the raw, served content different per user) and the server does not know the clients' filesystems. All resolution is client-side.

**WP1 — `internal/repoindex`.** `Scan(roots []string)`: walk with depth cap (default 4) and skip of heavy dirs (`node_modules`, inner `.git`, caches); for each repo it reads `remote.origin.url` and normalizes it to `host/owner/name` (handles scp-like ssh, https, `.git` suffix). Cache in `~/.config/cartographer/repos.json` with refresh on-miss. `Resolve(key)`: `paths:` map → cache → rescan → not found. Ambiguity: short name with multiple distinct remotes → error asking for the full form; multiple clones of the same remote → deterministic root order, first match + warning.

**WP2 — clientconfig.** `search_roots:` fields (default `["~/Documents"]`) and `paths:` (name→path map) in `Config`/`yamlConfig` (`internal/clientconfig/clientconfig.go:20`). Never on git: the file is already per-machine.

**WP3 — expansion at materialization.** `provisioning.Apply` expands the placeholders in the textual contents before writing to disk (write points: `provisioning.go:1017`, `:1779`, instructions block `:1283`). The **lockfile hash is computed on the expanded content** — drift detection compares the disk, it must compare like with like. Unresolved placeholder → warning on stderr and text left as-is (the sync does not block).

**WP4 — "Local paths" table in the instructions block.** At materialization the client collects the placeholders present in the KB's artifacts, resolves them and appends to the instructions block a key→local path table, plus the instruction for the agent: placeholder encountered in a concept and absent from the table → `cartographer resolve <key>`.

**WP5 — `cartographer resolve` subcommand.** `cartographer resolve repo:<...>|path:<...>` prints the resolved path: a runtime fallback for the agent (the binary is already on every connected machine) and a debugging tool.

**WP6 — `machine_path` lint (warning, server-side).** Flags home-anchored paths in concept bodies: `/Users/`, `/home/`, `~/`, `C:\Users\`. Deliberately narrow pattern: absolute container/cluster paths (`/etc/...`, `/var/...`) are legitimate and identical everywhere. Refined by [D124](data-plane.md#d124): a candidate inside a URL, or covered by a Map's `machine_path_allow_prefixes` allowlist, is a target-operational path rather than a client-local one and is not flagged.

**Rationale.** The git remote is the only identifier of a repo that is **already** shared and stable across the team's machines — using it as the key eliminates the one-to-one manual mapping that does not scale. Resolution lives in the client (scan+cache) and in the two channels the agent already has: the materialized imprinting (table) and the local binary (`resolve`). Discarded alternatives: pure manual mapping (does not scale, it was the v0 of this decision); server-side per-user profiles (identity and filesystem on the server side, hash breakage); resolution via env vars (explodes into N envs for N repos).

---

<a id="d116"></a>
## D116 — Trusted stdio MCP descriptors with environment references

**Decision.** MCP descriptors support a strictly disjoint `stdio` transport with a command, ordered
arguments and a reference-only environment map. Commands are bare executable names or clean absolute
paths; Cartographer emits command and arguments as separate native fields, never runs a shell, and
preflights the executable before changing any provider file or lockfile. The server allow-list binds
stdio descriptors to the exact command; D114 signatures and D115 hash-bound approvals continue to
bind the entire descriptor. Claude Code, Codex, Kiro and OpenCode receive their respective native
local-MCP representation; OpenCode translates `${VAR}` to `{env:VAR}`. No environment value is
resolved, persisted in a manifest/status/log, or sent back to the server.

**Rationale.** A local MCP server has a materially stronger capability than a remote endpoint: it
causes an agent runtime to execute a binary on the client. Keeping validation, server policy,
cryptographic identity, local approval and executable preflight as independent gates preserves the
fail-closed boundary while allowing portable KB descriptors. Retaining a bare command instead of its
resolved path preserves the client operator's intended `PATH` semantics.

**Discarded alternatives.** A single `command` string split by Cartographer: it would have
reintroduced shell-quoting ambiguity between the approved descriptor and the executed process.
Resolving `${VAR}` client-side before emission: it would have written secret values into provider
configuration files that are not designed to hold them.

<a id="d115"></a>
## D115 — MCP allow-list and hash-bound local approval

**Decision.** A KB-provided HTTP MCP descriptor is exposed only when an exact
per-KB operator allow-list entry matches its name, transport and normalized
absolute target URL. Empty policy denies all. A client then materializes an
unsigned descriptor only when a local record binds its source KB, artifact name
and full content hash; generic trust never applies. Cryptographic verification
remains independent and is sufficient on the client, but cannot bypass the
server allow-list.

**Rationale.** The server controls which endpoints a KB may advertise; the
client controls consent to execute one exact descriptor. Hash-binding covers
headers and environment references as well as URLs, and keeps either grant
from silently authorizing a changed endpoint.

---

<a id="d114"></a>
## D114 — Verify provisioning artifacts cryptographically

**Decision.** A KB may configure an operator-controlled 32-byte Ed25519 seed.
The server signs a deterministic, domain-separated binary envelope containing
format version, KB source, kind, name, version and canonical content hash. The
client pins public keys out of band per KB and verifies both reconstructed file
content (including paths and executable bits) and detached signature before
materialization. `Signed` is verification output only; bundle trust is the
separate `BuiltIn` origin.

**Failure semantics.** An absent signature is unsigned and remains eligible for
the existing explicit trust policy. A malformed, invalid, source-mismatched or
unknown-key signature, a duplicate conflicting signature, or a content-hash
mismatch is tampering: remote sync fails before provider files or lockfiles
change. Multiple client pins support rotation: pin the new key, switch the
server seed, then retire the old pin after clients have synchronized.

**Rationale.** A policy boolean sent by a server cannot authenticate an artifact
received over a compromised transport. Canonical length-prefixed bytes avoid
making JSON serialization the signed protocol and bind signatures to a precise
KB artifact identity, preventing replay across KBs, kinds or names.

---

<a id="d105"></a>
## D105 — Binary-safe provisioning artifacts and executable KB scripts

**Decision.** Provisioning artifact content is raw bytes; MCP read/write uses explicit `text`/`base64`
encoding and retains raw-byte `sha256` for `if_match`. Executability is derived from the KB filesystem,
not a descriptor. Hooks impose an effective mode: every file except `hook.json` is executable, while
`hook.json` is always non-executable. The versioned, domain-separated artifact hash includes that
effective mode.

**Rationale.** Git already versions the executable bit, so the KB remains the sole registry rather
than requiring a parallel metadata file. Including the effective mode in the hash makes `chmod` change
the manifest revision and realign existing installations; normalizing the hook floor avoids drift from
raw mode changes that cannot affect the materialized result. Base64 prevents byte corruption while
preserving the existing text API and `if_match` contract.

---

<a id="d138"></a>
## D138 — Provenance stamp on materialized skills and agents, and two hashes per managed file

**Decision.** A materialized `skill` (its `SKILL.md` only) and `agent` carry a marker-delimited
provenance block appended to the file, following the existing
`<!-- cartographer:provenance:begin … -->` / `:end` convention with the begin marker matched by
prefix. It names the source KB, the artifact's path in that KB, the artifact's content hash, and
the one remediation an agent can act on: local edits are replaced on the next sync, and the change
belongs in `artifact_write` on that KB at that path. Bundled artifacts are stamped too, stating the
Cartographer bundle as their source and offering no `artifact_write` instruction. Hooks, `mcp`
descriptors and `instructions` are never stamped. The block carries no timestamp and no manifest
revision, is rebuilt from the source content on every materialization (so re-stamping is a fixed
point and an older block is replaced, never nested), and is applied client-side only — the KB's copy
is untouched, like placeholder expansion.

`ManagedFile` gains `materialized_hash`: the hash of what was actually written, after expansion,
stamping and per-provider translation. `content_hash` keeps meaning "the manifest artifact's hash",
which is what `ComputeDiff` compares. An empty `materialized_hash` means unknown (any lockfile
written before this change) and is never a mismatch.

**Rationale.** A materialized skill was indistinguishable from a hand-written one: every other
managed artifact announces itself — the instructions block, the generated OpenCode plugin, the Codex
MCP block, the bootstrap script — but the two kinds an agent actually *reads* did not. So an agent
improving a skill had no address to send the improvement to, and no warning that its edit was about
to be overwritten; with several clients on one KB the round trip was undiagnosable from the file
itself. Naming the tool, the KB and the path inside the file is what makes the supported channel
reachable without documentation the agent may not have.

Separating the two hashes was a prerequisite, and fixes a latent defect on its own:
`copyArtifactFiles` returned the *expanded* hash and Apply stored it in `content_hash`, which
`ComputeDiff` then compared against the *manifest* hash — so any artifact containing a
`{{repo:…}}`/`{{path:…}}` placeholder compared unequal on every sync, was reported `Updated`,
rewritten, and showed as permanent drift in `cartographer status`. Artifacts without placeholders
were unaffected, which is why it went unnoticed; stamping would have made every skill and agent hit
that path, turning a corner case into the default.

---

<a id="d139"></a>
## D139 — On-disk verification: sync restores what diverged locally

**Decision.** Every `Apply` verifies the managed artifacts against the filesystem, not only the
manifest against the lockfile, and rewrites the ones that diverged. The server is the source of
truth: a restore is a rewrite, never a merge, with no backup copy — the supported way to change an
artifact stays `artifact_write` on the owning KB, which is what the D138 stamp tells the reader.
`cartographer sync --no-heal` reports divergence and skips the restore; it is off by default,
because the default must match what the stamp promises. `AppliedResult` reports restores as
`Healed` (separately from ordinary writes: a restore discards someone's local change) and, under
`--no-heal`, as `Divergent`. `cartographer status` counts on-disk divergence as drift.

Verification uses `ManagedFile.MaterializedHash` (D138), not the manifest hash: on disk sit the
expanded, stamped and provider-translated bytes, which never equal the source hash. An empty value
means unknown — a lockfile written before D138 — and is reported but never healed: treating it as
drift would rewrite every artifact on every client at once on the first upgrade. Scope per kind:
`skill`/`hook` re-hash their own directory (so an extra file inside, or a lost executable bit,
counts as modified); `agent` hashes its single file; `mcp` and `instructions` live inside files
shared with the user and are checked for **presence** of their managed key or marker block only —
their surrounding content is never compared and never rewritten. A read error is a finding, never
fatal.

`executableModeDrift` disappears into this general path. It existed because a chmod alone did not
change the content hash, and its comment already admitted the gap it was patching: "before this,
Apply could complete an unchanged manifest without reading its source at all". Now the materialized
hash is computed on the **normalized** modes the write applies, so mode drift is ordinary content
drift — its test survives as the regression guard.

**Rationale.** Drift detection never looked at the filesystem, so once the lockfile said an
artifact was applied its files were never read again: a skill an agent "improved" in place stayed
improved forever, a deleted file was never recreated, and `status` reported in-sync throughout. This
is the mechanical half of what D138 addresses editorially — without it, the stamp's "local edits
are replaced on the next sync" was simply false. Presence-only checking for `mcp`/`instructions` is
what keeps the pruning guarantee intact: those blocks live in files the user also owns, and
comparing their whole content would either produce permanent false drift or license rewriting
someone else's file.

---

<a id="d140"></a>
## D140 — A scheduled sync trigger for clients with no session hook; Kiro's hook deferred

**WP1 finding (checked 2026-08-27, no Kiro installation available on the machine).** Three facts,
from Kiro's own documentation:

1. **User-level hooks exist.** The CLI 2.13 changelog (<https://kiro.dev/changelog/cli/2-13/>)
   states: "Hooks placed in `~/.kiro/hooks/` now fire in every workspace automatically". The
   feature page (<https://kiro.dev/docs/hooks/>) still documents only `.kiro/hooks/` in the project
   root, so the two sources disagree on scope.
2. **A spawn trigger exists and is CLI-only.** The trigger table
   (<https://kiro.dev/docs/hooks/types/>) lists `agentSpawn` as available on the CLI and not in the
   IDE, described as firing when the agent is first activated.
3. **The literal trigger values in a hook file do not match that table.** The only complete JSON
   examples on the feature page use `"trigger": "PostFileSave"` and `"trigger": "Stop"` — while the
   table names those same triggers `fileSave` and `agentStop`. The exact string a spawn hook must
   carry is therefore **not determinable from the documentation**, and no installation was
   available to confirm it empirically.

**Decision.** WP2 (the Kiro session-start hook) is **not implemented**. `hook` × `kiro` stays
`unsupported` in the destination matrix. Writing a file into another tool's configuration
directory with a guessed trigger value produces a hook that is silently ignored — the exact
failure WP1 exists to prevent — and "silently ignored" is indistinguishable from "working" until a
user notices their KB skills are months stale. Implementing it needs one confirmation: the literal
`trigger` value a spawn hook carries, and that `~/.kiro/hooks/` is honoured by the CLI surface
where that trigger fires.

**Decision.** The scheduled trigger is implemented and is a first-class Layer 1 alternative:
`cartographer service sync-timer install|uninstall|status`, with `--interval` (default 30 minutes),
reusing the launchd/systemd machinery of the server service under its own label
(`com.cartographer.sync`, `cartographer-sync.timer`) and its own log destination. It is opt-in and
explicit: `connect` and `status` name the command once per invocation when a connected provider has
no session-hook mechanism, and never install it — putting a background job on someone's machine as
a side effect of connecting is out of proportion. It runs `cartographer sync` **without**
`--auto-trust`: an unattended job must not grant a trust the user never gave, while the persisted
`trust` setting still applies (D54). Install is idempotent; uninstalling what is not installed
succeeds.

**Rationale.** Kiro was the one supported provider syncing only when a human remembered to, and
D141's Hermes will be a second by design — its configuration is owned by an Ansible role, with no
place to register a hook. Both need the same thing: a trigger that does not depend on the client
having hooks. The timer serves them today and keeps serving Kiro's IDE surface even after the CLI
hook lands, since `agentSpawn` is CLI-only.

