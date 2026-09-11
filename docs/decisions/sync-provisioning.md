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
## D140 — A scheduled sync trigger for clients with no session hook; Kiro has no registrable one

**WP1 finding (documentation only, 2026-08-27 — no Kiro installation available at the time).** The
docs were self-contradictory: the trigger table (<https://kiro.dev/docs/hooks/types/>) named a
CLI-only `agentSpawn` trigger, while the only complete JSON examples on the feature page
(<https://kiro.dev/docs/hooks/>) used `"trigger": "PostFileSave"` and `"trigger": "Stop"` for
triggers that table calls `fileSave` and `agentStop`. The literal value a spawn hook must carry was
therefore not determinable, and WP2 was deferred rather than implemented on a guess.

**WP1 confirmed empirically (Kiro CLI 2.20.0, installed 2026-08-27).** Three facts, each verified
against the binary rather than the documentation:

1. **The trigger set is exactly `agentSpawn`, `userPromptSubmit`, `preToolUse`, `postToolUse`,
   `stop`** — the serde variant table in `kiro-cli-chat`, confirmed by `kiro-cli-chat agent
   validate`, which accepts those five and rejects `AgentSpawn`, `Spawn`, `SessionStart`,
   `sessionStart`, `agentStop` and `fileSave`. The documented `PostFileSave`/`Stop` examples are
   simply wrong for the CLI; the camelCase table is right.
2. **`~/.kiro/hooks/` plays no role.** It appears nowhere in the CLI's path table. Hooks are a
   `hooks` map **inside an agent config** — `~/.kiro/agents/<name>.json` globally, or
   `<workspace>/.kiro/agents/<name>.json` — each entry an object with a `command`. A global agent
   config is discovered from any working directory, and its `agentSpawn` hook does fire (verified
   with a sentinel file: "1 of 1 hooks finished").
3. **Hooks are per agent, and the agent that runs by default cannot carry one.** The hook fires
   only for the agent selected with `--agent`; running the default agent does not fire it. The
   built-in `kiro_default` cannot be shadowed by a global config of the same name (it stays
   `(Built-in)` and its hook never runs), and `~/.kiro/settings/cli.json` has no global hooks key —
   only display settings such as `hooks.showStatus`.

**Decision.** WP2 (the Kiro session-start hook) is **not implemented**, and this is now settled
rather than pending. `hook` × `kiro` stays `unsupported` in the destination matrix. The mechanism
the plan assumed — a hook file in a directory Cartographer owns — does not exist; the mechanism
that does exist cannot deliver an unconditional session-start hook, because the only way to reach
the agent the user actually runs is to write into an agent configuration Cartographer does not own.
Claiming a user's agent config to install a sync hook is a larger intrusion than the problem
warrants, and a Cartographer-owned agent nobody selects is a hook that never fires — the same
silent failure the deferral existed to prevent.

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
D141's Hermes is a second by design — its configuration is owned by an Ansible role, with no place
to register a hook. Both need the same thing: a trigger that does not depend on the client having
hooks. For Kiro the timer is not a fallback but the answer: `agentSpawn` is CLI-only and would
never have covered the IDE surface anyway, and it fires per agent rather than per machine.


## D148 — Provisioning refuses a symlinked destination

**Status: implemented (2026-08-28).** Closes #169.

**Context.** Materializing a KB's artifacts onto a destination that is a symlink followed
the link and wrote at the target. `os.WriteFile` on a symlinked path opens the *target* with
`O_WRONLY|O_TRUNC`; it does not replace the link. Reported from a field migration: the
client's skills directory was a symlink into an unrelated git checkout, and one
`cartographer sync` modified **23 files** there, each stamped with a provenance footer
declaring Cartographer as their source — false for those files, and in a shared repository
it lands inside someone else's commit.

The asymmetry is what made it a defect rather than a missing feature. The KB side already
guarded this: `internal/kb/asset.go` `Lstat`s every path component and refuses to traverse a
link. The client side performed 21 non-test `os.WriteFile`/`os.MkdirAll` calls across
`provisioning.go`, `hooksettings.go` and `bootstrap.go` with **zero** occurrences of
`Lstat`, `ModeSymlink` or `EvalSymlinks`.

Symlinked client-config directories are ordinary: a dotfile manager, a monorepo checkout, a
shared team directory, or an earlier bootstrap mechanism that linked skills out of a source
repository.

**Decision.**

- **Refuse, never resolve.** A symlinked destination is an error. Cartographer does not know
  what is on the other side and must not decide for the operator — and replacing the link
  with a regular file would be worse.
- The check is on the destination file **and every directory component under the base dir**
  (`ensureSafeDir`, mirroring `asset.go`'s component walk): the reported case was a symlinked
  *parent*, not a symlinked leaf. **The base dir itself is exempt** — it may legitimately be
  a link (a symlinked `$HOME`, a provider root from `BaseDirEnv`), so the walk starts below
  it.
- **The refusal is per artifact, not fatal to the pass.** One symlinked skill directory must
  not abort a whole sync, the same treatment `Apply` already gives an unsupported kind. The
  artifact goes to a new `AppliedResult.Refused` — distinct from `Unsupported` (the provider
  does support the kind) and from `NeedsApproval` (the artifact is authorized) — is reported
  as `refused: <kind>/<name>`, and is deliberately **left out of `Written` and out of the
  lockfile**, so the next sync retries and reports again. That is right for a condition only
  the operator can clear.
- The lockfile writes (`WriteLock`/`WriteLockFile`) are guarded too — a symlinked lockfile
  would make drift detection read and write someone else's state.
- `cartographer doctor` gains a `symlink` check over `ManagedDestinationRoots`, because
  `Apply` only mentions the condition on a run where the artifact is in the diff; afterwards
  it is invisible.
- **No git-awareness.** The stronger idea — refuse a destination that resolves inside a git
  repository other than the intended target — needs a repo-discovery walk on every write, and
  the symlink refusal already prevents the reported incident.

**Consequences.** Behaviour change: a sync that previously wrote through a symlink now
refuses that artifact, so an operator relying on a symlinked skills directory must repoint
the client base dir at the real location. `-dry-run` performs the same check and reports
`would refuse`, which is exactly when finding out is cheap.

## D154 — The generated steering block describes what this client received

**Status: implemented (2026-08-28).** Amends [D65](#d65) and [D141](#d141). Closes #175.

**Context.** The generated instructions described **intended** state rather than what provisioning
achieved on that client, so any partially-supported client got confidently wrong instructions.

`generateKBInstructions` emitted, whenever the KB declared any agent: *"Subagents installed by this
KB: <a>, <b> — their descriptions are in the client's agent registry: delegate to them the tasks
they cover."* The list came from `kbAgentNames(kbRoot)` — what the KB **declares** on disk. It could
not come from what the client received: the function has no provider argument and its only caller,
`BuildManifest`, runs exclusively server-side, where no provider is known. Meanwhile whether a
provider can receive an agent at all is a static fact the code already states —
`destinationMatrix["agent"]` marks `kiro` and `hermes` `unsupportedDest`. So a KB with two agents
synced to Claude Code and Kiro materialized them for the first and **not at all** for the second,
while both were told they were installed. On the reporting deployment the only agents in that
client's registry were stale files from an earlier bootstrap, still pointing at a decommissioned
source.

**Two corrections to the field report, made while implementing.**

*The omission was not silent, it was not persistent.* `printApplySummary` does print
`unsupported: agent/<name> … (kind has no destination for this provider)` for every artifact routed
to `Unsupported`. What is missing is persistence: the line appears only on a run where the artifact
enters the diff, `doctor` never reported the condition, and the steering sentence was wrong
regardless of any output — which is the part that misleads the model rather than the operator.

*"Written once per mounted KB" is a reporting defect, not three writes.*
`applyInstructionsGroup` accumulates every KB's snippet and calls `writeInstructionsBlock` **once**;
it then appends one `ManagedFile` per instructions artifact — i.e. per KB — and the summary printed
one line each. Nothing was rewritten N−1 times, so the fix belongs in the printer.

**Decision.**

- **The subagent sentence moves out of the hashed artifact and is emitted client-side, per
  provider.** There is an exact precedent in the same function: the D75 WP4 local-paths table is
  appended there, client-side only, and deliberately not part of the artifact hash. The sentence has
  the same nature — a statement about *this client*, not about the KB.
- It lists only agents whose kind has a destination for the provider, and only those actually
  authorized: an artifact awaiting approval was not installed, and the whole point is that the
  sentence describes what happened.
- **The rewrite trigger now fires on an `agent` artifact too.** The block's rendered content depends
  on the agent set while its artifact hash deliberately does not, so without this the sentence would
  go stale the moment an agent was added or removed. The test that asserted the hash changes when an
  agent is added now asserts the opposite, and names the trigger as the guarantee that replaced it —
  the invariant moved, it did not disappear.
- **A declared kind the provider cannot receive is warned about on every run**, computed from the
  manifest rather than the diff.
- **The wrapper becomes opt-out, not translated.** `instructions.md` may declare
  `<!-- cartographer: preamble: none -->` on its first line to suppress the three generated
  bullets — the redundant, hardcoded-English part. Deliberately **not** a localisation mechanism: a
  `language:` key would make Cartographer carry translations of its own prose forever, so the KB
  owns the prose instead. **The one-line routing sentence stays generated in every case** (KB name,
  server, archives): it is state, not prose, and no KB can write it for itself. The residual is
  therefore one English line rather than none, and this entry says so rather than claiming the
  language break is gone. The directive is honoured on the first line only, so a KB can document it
  without triggering it — the same trap as the placeholder syntax.
- **Reporting collapses per path for the instructions kind only.** `result.Written` keeps one entry
  per artifact, because the lockfile and drift detection depend on it; elsewhere two artifacts never
  share a path and a blanket dedup would hide a real bug.

**Consequences.** The steering block changes content on clients that cannot receive agents — the
false sentence disappears — so those clients rewrite the block once on the next sync, which is the
normal drift path. New KB-side directive; no config migration.

---

<a id="d171"></a>
## D171 — A cross-KB collision is an error, not an alphabetical tie-break

**Decision.** `provisioning.DetectCollisions` reports every `kind`+`name` claimed
by two or more distinct `kb:` sources, and `MergeArtifactsStrict` turns that into
a `*CollisionError` naming the kind, the name and the claiming KBs. The client
merges through the strict variant (`fetchMergedManifest`), so a collision stops
the sync before anything is materialized. `MergeArtifacts` keeps its tolerant
behaviour for `BuildManifest`. `cartographer client bind` warns when a new
binding creates a collision, and `doctor` gains a `kb-collisions` check.

**Rationale.**

- **The silent resolution was unstable, not just undocumented.**
  `MergeArtifacts` deduplicates on `kind+name` *without* the source, and
  `preferArtifact` picked the alphabetically first `kb:` source. Nothing recorded
  that a second copy existed. Worse, unmounting the winning KB makes the losing
  one appear: the content an agent reads changes with no artifact, no hash and no
  revision having changed in the KB anyone was looking at.
- **Error, not warning.** A warning about an artifact the agent then actually
  loads is worse than a failed sync, because at that point the wrong answer is
  silent. The failure names both KBs and the remedy, which is a rename.
- **KB-over-bundle stays.** It is deliberate — a KB may override a bundled skill
  — and unambiguous, because the bundle is single. Only KB↔KB became an error.
- **Two functions, not a flag.** `BuildManifest` merges one KB plus the bundle,
  where the case cannot arise; giving it a strictness parameter would add a
  branch nobody can reach. `MergeArtifactsStrict` is the client's entry point and
  the tolerant one keeps its callers.
- **`fetchMergedManifest` split into `fetchCandidates` + merge.** The per-KB
  responses have to survive the pull for the collision to be attributable at all,
  and the same unmerged candidates are what `client bind` and `doctor` filter per
  provider. This is also the seam the filtered projection needs.
- **Per provider, not globally.** Two colliding KBs bound to two different
  providers are not a conflict. `collisionsForProvider` narrows a collision to
  the KBs bound to one provider, so the warning does not train an operator to
  ignore it.
- **`client bind` stays offline.** The collision check needs the server, so it is
  best-effort: unreachable means "check skipped", not a failed command.
  Configuring a machine must not require the network, and the sync-time refusal
  is the backstop.

**Known limitation, since closed by [D172](#d172).** As written here, `runSync`
rewrote the providers' MCP entries *before* fetching the manifest, so a refused
merge still left those rewritten; only artifacts and the lockfile were protected.
A test pinned that behaviour so the reorder could not land silently. D172
reordered `runSync` and inverted the assertion: a refused merge now leaves the
MCP entries untouched as well.

**Consequences.** A deployment with two colliding KBs — working today, silently
and arbitrarily — starts failing its sync with a report and a remedy. That is the
intent. Everything else is unchanged: single-KB clients and clients whose KBs
have distinct artifact names never reach the new code path.

---

<a id="d170"></a>
## D170 — Selection before the merge: each provider is projected only its bound KBs

**Decision.** The client keeps the per-KB `sync_pull` responses unmerged
(`candidateSet`), and builds one manifest per provider: its bound KBs' responses
→ `SelectForSources` → `MergeArtifactsStrict` → `VerifiedManifest` →
`FilterForProvider` → `Apply`. `FilterForProvider` recomputes the revision,
`ManagedFile` records the artifact's `Source`, MCP entries are emitted per
provider, and `sync` gains a repeatable `--client`.

**Rationale.**

- **The filter had to move before the merge.** Filtering the merged manifest by
  source is wrong, and the failure is silent data loss rather than a visible
  error: `MergeArtifacts` deduplicates on `kind+name` and prefers a `kb:` source
  over `bundle`, so if `kb-A` overrides a bundled skill and a provider is bound
  only to `kb-B`, the merge keeps `kb-A`'s copy and the source filter then
  deletes it — the provider ends with **nothing** where it should have received
  the bundled copy. The server performs the same KB-over-bundle merge inside each
  single-KB pull, so the discarded candidate cannot be reconstructed client-side.
  That is why `fetchCandidates` returns responses keyed by KB.
- **The revision had to be recomputed.** `FilterForProvider` used to inherit
  `m.Revision`. With per-provider sets that makes `ComputeDiff.InSync` compare
  two different manifests under one string, and — worse — changing a binding
  would not change the revision, so no drift would ever be detected. The lock now
  records the revision of the manifest actually applied, which also fixed a
  pre-existing incoherence for providers that support only a subset of kinds.
- **Unbinding needs no removal logic.** An unbound KB's artifacts leave the
  provider's manifest, so `ComputeDiff` marks them `Removed` and `PruneManaged`
  deletes them. The prune only touches files listed in the lock, so user-owned
  files survive. This is why the projection was built as a filter rather than as
  a separate teardown path.
- **Entry shape and entry set are different questions.** A bare `/mcp`
  auto-routes only when the *server* mounts exactly one KB
  (`MultiKBServer.Handler`); with four mounted and one bound, the client still
  needs `?kb=`. `entriesForKBs` therefore takes both lists. Symmetrically,
  `removeMCPEntries` keeps receiving the **union** of every known KB: it derives
  the names this client may own from the list it is given, so a filtered list
  would orphan an unbound KB's entry permanently. Both are pinned by tests.
- **The default resolves against live discovery, not the cache.** A provider
  with no binding receives every KB the server currently mounts, read from
  `/health` rather than from `known_kbs` — `connect` runs before that cache is
  refreshed, and a dry run never refreshes it, so using the cache produced an
  empty entry set on both paths.
- **A nameless endpoint disables bindings rather than starving the client.**
  When the server does not identify its KBs by name, no binding can match. Every
  provider then receives everything, with a warning, exactly as before this
  change — a missing signal is not a reason to strip a client. The one exception
  is a provider explicitly bound to no KBs, where the declaration is unambiguous.
- **`--client` leaves the others completely untouched**, including their MCP
  entries and their lockfile entry, so a targeted sync cannot half-migrate a
  provider nobody asked about.
- **`ManagedFile.Source` is `omitempty` and "empty means unknown".** Every
  lockfile written before this change has none; treating that as "wrong
  provenance" would make `doctor` flag every existing machine on upgrade, and
  `ComputeDiff` still compares `ContentHash` only, so nothing re-materializes.

**Consequences.** Every client's revision changes once, on the first sync after
release, because it is now computed after filtering — that is a recomputation,
not drift, and it must be called out in the release notes. Behaviour is otherwise
unchanged for anyone who declares no binding. `statusManifestFn` became
`statusManifestsFn` (per provider); `materializeForProviders` takes a
per-provider manifest map, with `uniformManifests` for the callers that
legitimately have one.

---

<a id="d172"></a>
## D172 — Sync ordering, per-provider checkpoints, and a client lock

**Decision.** `runSync` writes nothing before the manifest is fetched and
verified; `materializeForProviders` checkpoints the lockfile after every
provider; every path that mutates client state takes an advisory OS file lock;
and `--dry-run` covers the removals and the `known_kbs` rewrite it used to hide.

**Context.** Three distinct defects, none of which is "the sync is not atomic" in
general — that framing hides which write is actually unprotected.

- **Order.** `runSync` removed and rewrote the providers' MCP entries and saved
  `.cartographer.yaml` *before* `fetchMergedManifest`. A failed `sync_pull`, an
  unverifiable signature or a cross-KB collision ([D171](#d171)) left those
  writes in place, and the error said nothing about it. The artifact writes were
  already correctly ordered — `ensureBootstrapForProviders` sits after the
  manifest check deliberately, and stays there. The fix moves the configuration
  writes to the same side of that line and makes the failure message say that
  nothing changed, because a user who cannot tell what state the machine is in
  will re-run blind.
- **Checkpoints.** `Apply` ran per provider while the lockfile was written once
  at the end, so a failure on provider N left providers 1..N−1 with files on disk
  and **no lock entry**: unmanaged files that pruning never removes and `doctor`
  cannot see. The lockfile is now written after each provider, and the error
  names the ones already recorded so a rerun is informed. N atomic renames
  instead of one, with at most five providers: a deliberate trade of I/O for
  safety.
- **The guarantee, stated rather than implied.** A failure *between* steps leaves
  a consistent state and a completed provider is always recorded. This does
  **not** make a single `Apply` atomic — the failed provider's own partial files
  are still possible, which is [D178](#d178)'s subject. Saying so is the point:
  the alternative was a generalized snapshot-and-rollback, and rolling back the
  native configs of four providers is more dangerous code than it removes.
- **The lock is at OS level.** Nothing serialized concurrent syncs, and every
  path did a read-modify-write of the lockfile, so two of them lost each other's
  provider entries, last writer wins. This is not hypothetical: the bootstrap
  hook runs `cartographer sync` at session start, so several agent sessions
  opening at once produce concurrent *processes* — which an in-process mutex
  would not see. `.cartographer-client.lock`, `flock`, released on every exit
  path, with a bounded wait that fails naming the file: a sync that silently
  loses an entry is worse than one that asks to be rerun. A dry run writes
  nothing and takes none.
- **The TUI stops fanning out.** `S` ran one `syncCmd` per provider through
  `tea.Batch`. With the lock in place each goroutine would only queue behind the
  others, buying nothing while making the progress reporting incoherent, so it
  runs them sequentially under one lock and reports a partial failure as such.
- **A plan that hides its removals is not a plan.** `--dry-run` showed the files
  and the MCP entries it would add, but not the ones it would remove nor the
  `known_kbs` rewrite — exactly the destructive half. Both are now printed, and a
  `--client`-restricted plan says so in its header, since read without the
  command line that produced it a partial plan looks complete.

**Consequences.** No interface change. Failure behaviour is more conservative,
and there is one new failure mode: two overlapping syncs, where the second now
reports the lock instead of silently corrupting the first one's state.

---

<a id="d178"></a>
## D178 — A file dropped from an artifact is removed with it, not stranded

**Decision.** When a multi-file artifact is rewritten, `Apply` removes the files
it owned in the previous lock and no longer declares, through the ordinary prune.
`doctor` reports — and never deletes — files inside a managed directory that no
lock entry accounts for.

**Context.** Two paths combined into a silent leak. `copyArtifactFiles` wrote
what the artifact currently carries and never removed what it no longer did;
`Apply` then rebuilt `newManaged` from the write, dropping the entries for the
files that disappeared. The result stayed on disk **and** left the lock, so
pruning, `ComputeDiff` and `doctor`'s on-disk verification were all blind to it.

Beyond tidiness: for a skill or an agent, an orphan inside a live artifact
directory is still read by the agent — a reference file deleted from a KB skill
kept being loaded indefinitely, with no drift signal — and an orphaned hook
script may still be invoked by a registration that outlives it.

- **In `Apply`, not in a cleanup command.** A sync that leaves a known orphan
  behind and relies on a later `doctor --repair` is a sync that lied about being
  in sync.
- **Only what this artifact previously owned.** The removal set comes from the
  previous lock's entries for that `kind+name`, minus what was just written, and
  is further restricted to paths under the artifact's own destination directory.
  Deleting by directory listing would take a file the user placed there — the
  guarantee the instructions prune is already careful to keep — and would
  mistake a hook's generated plugin file, written elsewhere, for a dropped one.
- **Reported like any other prune.** It goes through `PruneManaged`, so empty
  directories are cleaned and the removal shows up in `printApplySummary` and in
  the `--dry-run` plan. A silent deletion is not an improvement over a silent
  orphan. Under a dry run the comparison uses the artifact's declared files
  rather than the simulated principal path, or the plan would list every other
  file of the artifact as removed.
- **Pre-existing orphans are reported, not deleted.** Files stranded by earlier
  versions are in no lock, so nothing can prove Cartographer wrote them: `doctor`
  names them with a remedy and leaves them alone. They disappear naturally at the
  next sync once the artifact owns the path again.

**Deliberately not implemented.** The plan also proposed removing the files of an
artifact that becomes **unauthorized** mid-update, by analogy with the
force-pruned unauthorized `mcp` entries. That analogy does not hold: an MCP
approval is explicit and revocable per hash, so its withdrawal is an intentional
signal, whereas an unsigned KB skill is unauthorized on **every** sync that does
not pass `--auto-trust` and has no persisted `trust`. Applying the rule there
would delete a legitimately installed skill on every ordinary sync. The
unauthorized case therefore keeps today's behaviour: the artifact stays on disk
and is reported as needing approval.

**Consequences.** The first sync after release removes files that were orphaned
**and** are re-owned by a rewritten artifact; older orphans are reported by
`doctor`, not deleted. Both belong in the release notes.

---

<a id="d181"></a>
## D181 — A cached repo path is validated before use, and a change of roots invalidates the cache

**Decision.** `repoindex.lookupIndex` no longer serves a cached candidate path
as-is: each candidate's paths are filtered down to the ones that currently hold
a live clone (a directory containing a `.git` entry, checked with the
symlink-following `stat`, one call per candidate), and ambiguity/emptiness is
evaluated on the survivors, not on the raw cache. `Resolve` also compares the
cache's `Index.Roots` against the `roots` argument (each element normalized
with `expandHome`, order-sensitive) before trusting the cache at all — a
difference skips `lookupIndex` entirely and falls through to a fresh `Scan`.

**Context.** `Resolve` served a cache hit with no filesystem check: moving a
clone left every `{{repo:<key>}}` citing it resolving to the old, now-wrong
location, silently and indefinitely — `service restart` did nothing, since the
stale value lives in `~/.config/cartographer/repos.json`, not in server memory.
Only a deliberate miss (resolving a nonexistent key) rewrote the cache, by
accident. Two adjacent gaps made it worse: `Index.Roots` was persisted and never
read, so editing `search_roots` didn't invalidate anything either; and with
multiple clones of the same remote, a dead first-in-root-order entry kept
winning over a live second one. Worst case, a leftover clone at the old
path (an old copy, a `.Trash` copy) made the wrong resolution look like a
correct one — an agent following the placeholder read, or committed into, the
wrong working tree with nothing to signal it.

- **Refresh-on-miss stays the only refresh path (D75).** No watcher, no daemon,
  no TTL: a stale hit now degrades to a miss, which was already the trigger for
  a rescan. The behaviour that repairs staleness is the one D75 already
  specified — it just never used to fire.
- **A hit stays cheap.** Validation costs one `stat` per candidate path
  (following a symlinked clone correctly), never a filesystem walk — the depth
  cap exists precisely to keep `Scan` off the common resolution path, and this
  does not reintroduce it.
- **Root order still decides the winner** among multiple live clones; the
  filter only removes candidates that are not really there anymore.
- **An ambiguous short name that is only ambiguous on paper — because one of
  the colliding remotes has no live clone left — now resolves instead of
  erroring.** That is the intended fix, not a side effect: the ambiguity was an
  artefact of a stale index, and D75's "ask for the full form" rule exists to
  disambiguate remotes that really do coexist on disk.
- **An empty-vs-nonempty `Roots` counts as a difference.** A cache written
  before this change carries no `Roots`, so the very first resolution after
  upgrade always rescans once — an intended, one-time cost, not a bug.

**Consequences.** No interface, configuration, CLI or MCP surface change — this
is a behavioural fix. The first resolution after upgrading to this version
rescans once per cache, per the `Roots` edge case above. `fix:` release.

---

<a id="d182"></a>
## D182 — Attribute and order the per-KB instruction sections

**Decision.** Each KB's snippet inside the shared instructions block is wrapped
in its own named markers, `<!-- cartographer:kb:<name>:begin -->` … `<!--
cartographer:kb:<name>:end -->` (`wrapKBSection`) — a marker family distinct
from the outer `cartographer:instructions:begin/end` pair, so the malformed-
block check that counts occurrences of the outer markers keeps counting
exactly one of each regardless of how many KBs contribute. Immediately before
its curated body, and only when curated content exists, `generateKBInstructions`
emits a one-line scope sentence naming the KB and stating that its directives
govern its own perimeter and that the more specific source wins on a conflict
with another KB's directives or a repository's own instruction file. No
markdown heading is introduced around the curated body — Cartographer wraps,
it does not edit, per [D61](#d61)'s and [D154](#d154)'s principle that the KB
owns its prose. Section order follows the provider's explicit KB binding
(`clientconfig.ClientBinding.KBs`, [D170](#d170)) when there is one — carried
into `provisioning.Apply` as the new `ApplyOptions.KBOrder` — falling back to
alphabetical by KB name otherwise; a KB present in the manifest but absent
from the binding sorts alphabetically after the declared ones, so a reorder
can never drop a section.

**Context.** `generateKBInstructions` emitted, per KB, a routing line, the
operational bullets, and the curated `instructions.md` verbatim, with nothing
marking where one KB's voice ended and the next began — a directive written
for one KB's perimeter reached the agent as an unqualified, session-wide rule.
Section order was alphabetical by KB name, an accident of directory naming
rather than a declared choice, even though position was already known to
matter: [D154](#d154) moved the generated preamble ahead of a curated body in
another language specifically because the first thing the model reads is the
worst position for an inconsistency, since it sets the expected output
language. With several KBs, whichever one happened to sort first occupied
that same position.

**Why [D171](#d171) cannot catch this.** `DetectCollisions` reports a
`kind`+`name` claimed by two `kb:` sources, and for `kind: instructions` the
`Name` *is* the KB name — unique by construction. Two KBs can never collide on
this kind, so the strict merge is structurally blind to a session-wide
directive smuggled into one KB's curated prose. D171's remedy for a real
collision is "rename one of them"; prose has no rename. This plan does not
attempt to adjudicate the meaning of two conflicting prose directives — that
is not mechanisable. It makes every directive attributable and scoped, and
makes precedence declared instead of accidental, which is what lets a model
apply the ordinary "more specific source wins" rule. Declared session-global
directives with a key — so that two KBs asserting the same key with different
values become a *detectable* collision under D171's "error, not warning"
stance — is deliberately left to a follow-up issue (#233): it needs a new
authoring convention in `instructions.md` plus plumbing in `DetectCollisions`
and touches the same file as this change, so it lands strictly after it.

**Rationale.**

- **A single managed region, rebuilt from scratch.** The per-KB markers live
  *inside* the existing outer block (`instructionsBlockBeginPrefix`/`End`,
  [D56](#d56)), which stays the only region `writeInstructionsBlock` ever
  replaces; a removed KB still leaves no residue.
- **The scope sentence is generated content, not envelope.** It is written by
  `generateKBInstructions`, so it flows into `Artifact.ContentHash` like the
  rest of the block — existing clients see one instructions update on the
  first sync after upgrade, and it is self-limiting.
- **A reorder alone still has to rewrite the file.** Same KB set, same
  content hashes, only the sequence moved: invisible to `ComputeDiff`'s
  Added/Updated/Removed. `applyInstructionsGroup` compares the previous run's
  recorded section order — the sequence of `instructions` entries already
  carried in the incoming `Lock`, needing no new persisted field — against the
  newly computed one, and treats a mismatch as its own trigger
  (`instructionsOrderChanged`).
- **Uniform shape, not a conditional.** A single-KB client gets the delimiters
  and the scope sentence too, and a KB that opted out of the generated bullets
  (`preambleNoneRe`, [D154](#d154)) still gets both — the opt-out is about the
  bullets, not about attribution. The same file shape is what `doctor` and any
  future parser can rely on.
- **The recognizer matches a full line.** The per-KB markers are generated
  from the KB name, already sanitised by `config` before it reaches
  `BuildManifest`; curated content containing a similar-looking line embedded
  mid-paragraph is not itself a marker line, and nothing re-parses the body to
  look for one, so it cannot forge a section boundary.

**Consequences.** `fix:` — the generated block changes shape on every
provider, so every connected client shows one instructions update on the
first sync after upgrade. No configuration, CLI or MCP surface change; no KB
content is modified, ever — only wrapped.

---

<a id="d183"></a>
## D183 — Keyed session-global directives make a cross-KB prose conflict detectable

**Decision.** A curated `instructions.md` may declare a session-global
directive with a key anywhere in its body: `<!-- cartographer:directive:<key>:<value>
-->`, recognised as a full line (after trimming) so a KB can document the
syntax itself without triggering it — the same defensive shape as
`preambleNoneRe` and D182's `cartographer:kb:<name>:begin/end` markers, and
the same D163 metasyntax trap both had to account for. `<key>` and `<value>`
are each required to be non-empty and to contain no `:`, so the split stays
unambiguous; a malformed line (empty key or value, an extra `:` inside either
part) is not recognised and is left as ordinary prose. Lines inside a fenced
code block (` ``` `/`~~~`) are never eligible either, so a marker shown alone
on its own line as a worked example — the natural way to document the syntax
— does not declare anything; full-line matching alone only protects a marker
embedded mid-paragraph. `DetectDirectiveCollisions`
scans every `kind: instructions` artifact's generated content for these lines
and groups the declared `(key, value)` pairs by key; `MergeArtifactsStrict`
fails with a `*CollisionError` naming the key, every declared value, and the
KB that declared each, when two `kb:` sources in the same merge disagree on a
key's value. Two sources declaring the same key with the same value are not
reported — they agree, there is nothing to adjudicate. The recognised line is
**not** stripped from the rendered block: unlike `preamble: none`, which is a
control signal meaningful only to the generator, a directive's value is
content an agent reading the block may want to see verbatim, and stripping it
would cut against D182/D154's byte-identical delivery of the curated body.

**Context.** D182 made a cross-KB prose conflict *attributable* — an agent
reading the block now knows which KB said what — but left it undetectable by
the server or the client: for `kind: instructions`, `Name` is the KB name,
unique by construction, so `DetectCollisions` ([D171](#d171)) can never see
two KBs claim it. #233 asked for a narrower, mechanisable slice of that gap:
not adjudicating arbitrary conflicting prose (still not mechanisable, still
left to "the more specific source wins"), but letting a KB opt a specific
fact into structured comparison by giving it a key.

**Rationale.**

- **Comparison happens on the already-provider-scoped slice, no new
  plumbing.** `DetectDirectiveCollisions` takes the same `[]Artifact`
  `DetectCollisions` does, and every caller already narrows that slice to one
  provider's bound KBs before calling `MergeArtifactsStrict`
  ([D170](#d170)/[D171](#d171)); two KBs that never reach the same client
  already cannot collide on `kind`+`name` for the same reason, and directives
  inherit it for free.
- **Extraction reads `Artifact.Files`, not a new wire field.** The `kind:
  instructions` artifact's content — generated per KB, curated body included
  — already travels through `sync_pull` in `Files[0].Content` for the on-disk
  write. Re-scanning that content at merge time needed no change to the
  `Artifact` struct, the `sync_pull` JSON shape, or a KB's materialized
  files: strictly additive. The generated wrapper prose around the curated
  body (fixed, hardcoded English, no HTML comments) can never itself match
  the marker, so scanning the whole generated artifact is equivalent to
  scanning only the curated section.
- **Fence-aware, not just full-line.** Full-line matching alone stops a
  marker embedded mid-paragraph but not one shown alone on its own line
  inside a fenced code block — the natural way to document a syntax by
  example. `extractDirectives` tracks fence state with the same pragmatic
  CommonMark subset `okf.headingEligibleLines` uses for the identical
  problem with markdown headings (kept as a small local duplicate rather
  than an export from `okf`: one caller, no other reason for the two
  scanners to share code).
- **Last declaration wins within one KB's own prose.** A KB repeating the
  same key twice with two different values in its own `instructions.md` is
  not a cross-KB collision — there is only one author to ask, and
  `extractDirectives` keeps the last one, silently, the same way a later
  assignment would read in ordinary prose.
- **Error, not warning, matching D171.** A warning about a directive the
  agent then actually reads is worse than a failed sync, for the same reason
  D171 gives for a structural collision: at that point the wrong answer is
  already silent. `CollisionError` grew a second section instead of a second
  error type, so a caller printing "Error: %v" still gets one self-contained,
  actionable report even when both kinds of collision fire in the same
  merge.
- **Visible, not stripped.** `preamble: none` is removed because it is a
  control signal aimed at the generator, not at the reader — leaving it would
  read as a stray, uninterpreted instruction. A directive's value is the
  opposite: it is the KB's own claim about a session-wide fact, and hiding it
  would make the rendered block disagree with what
  `DetectDirectiveCollisions` just verified about it.

**Consequences.** `feat:` — new authoring convention, and no existing curated
body matches it by accident (the marker's tight `<key>:<value>` shape, no
extra `:` or empty part tolerated, keeps a KB's own prose about markers from
tripping it, same as D163/D182). A deployment with two KBs bound to the same
provider that declare the same directive key with different values — working
today, silently, left to a human to notice and resolve via "the more specific
source wins" — starts failing its sync with a report naming the key, both
values and both KBs. Everything else is unchanged.

---

<a id="d184"></a>
## D184 — `sync` reports the revision each provider recorded, not the one it fetched

**Decision.** `printSyncRevisions` and `commonRevision` (`cmd/cartographer/sync.go`)
take `map[string]provisioning.AppliedResult` — `materializeForProviders`'s return
value — instead of the pre-projection `map[string]provisioning.Manifest`, and read
`AppliedResult.NewLock.AppliedRevision`: the same value `Apply` writes into the
lockfile and `status` compares against, rather than recomputing `FilterForProvider`
independently at the call site. The dry-run path takes the same values: `Apply`
fills `NewLock.AppliedRevision` from the post-filter manifest whether or not
`DryRun` is set, so a plan and the real run that follows it name the same number.

**Context.** `printSyncRevisions` was handed `manifests`, the map
`manifestsForProviders` returns *before* per-provider projection. The
projection — `FilterForProvider`, which drops kinds a provider has no
destination for and recomputes the revision over what remains ([D170](#d170))
— happens one level down, inside `materializeForProviders` → `Apply`. For a
provider that supports less than the full kind set — `hermes` is the sharpest
case, only `skill` ([D141](client-configurator.md#d141)) — `sync` printed a revision no other
command would ever produce: `status` reads `lockfile.applied_revision`, itself
set from the *filtered* manifest at `Apply` time. Reproduced on a
`hermes`-only client, `sync` and `status` disagreed on every single run with
nothing actually out of sync — the [D147](client-configurator.md#d147)
failure shape again (output derived from the command's intent, not its
outcome), on the very provider D147's own writeup already names.

**Rationale.**

- **Read the recorded value, don't recompute it.** `materializeForProviders`
  already returns the applied result per provider; sourcing the printed
  revision from `AppliedResult.NewLock.AppliedRevision` leaves exactly one
  place that computes "what does this provider's revision look like" —
  inside `Apply`, via `FilterForProvider` — instead of a second, independent
  computation at the reporting site that could silently drift from it again.
- **`commonRevision`'s agree/diverge logic is unchanged.** Only the source of
  the numbers it compares changed; unsupported-kind filtering is simply a
  second cause of the same per-provider divergence the function already
  handles for bindings ([D170](#d170)).
- **Dry run and real must not diverge.** `Apply` computes
  `NewLock.AppliedRevision` from the filtered manifest before branching on
  `DryRun`, so `--dry-run` and the real `sync` that follows it name the same
  revision for an unchanged manifest — the invariant D147 established for the
  verb (`would sync to`) now also holds for the number it precedes.

**Consequences.** `fix:` — output only; no configuration, CLI or MCP surface
change. A provider with unsupported kinds now prints the same revision
`status` reports for it.

---

<a id="d193"></a>
## D193 — Workspace-scoped artifact projection: one provider, different KBs per repository

**Status: implemented.** Closes #247.

**Context.** A session working in the HomeLab perimeter activated `new-microservice`, a skill
belonging to `dante-kb` and specific to DANTE/ADMS. Cartographer tracked the source KB correctly
everywhere it records provenance — the manifest, the lockfile's `source` (D170), the materialized
footer — but the provider chooses a skill from **name and description alone**, and the provenance
block is in the body, read only *after* activation. The description was generic and did not name
DANTE.

D169/D170's per-provider binding closes this for a provider dedicated to one perimeter. What it
cannot close is the case that produced the incident: the binding is **static per provider** and does
not depend on the current directory. `.cartographer.yaml` is machine-wide by design, every provider
destination is under `$HOME`, there is no notion of an active workspace, and two concurrent sessions
of the same provider in two perimeters share one global catalogue.

The audit verified that every supported provider *does* have a project-local scope — Claude Code
`.claude/`, Kiro `.kiro/`, OpenCode `.opencode/`, Codex `.agents/skills` and `.codex/` along
cwd→repository root — and that this holds for instructions, agents, hooks and MCP descriptors, not
only skills. Sources are recorded in `docs/interoperability.md` §Project-local scopes.

**Decisions.**

- **An explicit binding between workspace and KBs, per provider**, with the scope selected per
  provider. Absent means the historical global catalogue, so no existing installation changes on
  upgrade; migration is a deliberate `workspace bind` or a `connect --workspace`.
- **The bind takes a path, resolves it once, and persists the canonical form.** For a git repository
  it also records the normalized remote as a **guard** against moves and accidental reuse — never as
  a selector, because one remote has many clones and worktrees and choosing between them by remote
  would pick the wrong one. A bound path that is gone, or whose remote has changed, is an **error**.
- **Fail closed, everywhere.** An unbound workspace receives the transversal bundle and no KB
  artifact. "No KBs" and "several KBs" are distinct explicit states and neither is ever "every KB" —
  `workspace bind` requires `--kb` or an explicit `--no-kb` rather than defaulting. A provider with
  no project-local scope (hermes, antigravity) **cannot** be bound and is refused with the reason,
  never degraded to the global catalogue: degrading is precisely the exposure this closes.
- **Only Cartographer's own transversal bundled skills stay global.** `cartographer-ops`,
  `kb-create` and their siblings belong to no perimeter, and a session outside every bound workspace
  still needs them. Nothing KB-sourced is materialized globally under workspace scope, and nothing
  bundled is copied into a workspace — the E2E scenario caught the first implementation doing
  exactly that, which would have multiplied `cartographer-ops` by the number of bindings.
- **The lockfile gets a separate namespace, not an extended key space.** Prune deletes every managed
  file its lock names, so a key that *could* resolve to the wrong projection deletes another
  workspace's files. Two maps — `providers` and `workspaces` — make that mistake unrepresentable
  rather than merely unlikely. Each workspace lock carries its `base_dir` and a `project_scope` flag,
  the latter persisted rather than derived because verifying or pruning through the wrong matrix
  looks for the wrong paths entirely.
- **Unbinding prunes.** The declaration and the files are two different things: a later sync only
  iterates the *declared* projections, so without an explicit orphan pass the files of an unbound
  workspace would stay in that repository forever. The pass considers workspace projections only —
  a provider's global catalogue is `disconnect`'s decision, and reaching it from here would delete a
  connected provider's artifacts because it was absent from one `sync --client`.
- **Collision detection is scoped to the projection, not weakened.** Two KBs owning a same-named
  skill in two different workspaces is legal and now works; the same two bound to **one** workspace
  is still refused by the strict merge (D171), because that is the only place a collision can
  actually confuse an agent.
- **Repository hygiene is two rules with teeth.** Cartographer excludes only its **own untracked**
  paths, only in `.git/info/exclude`, inside a marker block — never `.gitignore`, which is shared
  with everyone who clones the repository. A shared file the repository already tracks is the
  user's: the block goes inside it and it is not excluded; one Cartographer created itself is
  untracked and is excluded, or every sync would leave the working tree dirty. A path Cartographer
  would own **entirely** that git already tracks is a refusal, before anything is written.
- **Honest reporting where correct files are not enough.** Codex ignores a project's `.codex/` layer
  unless the project is trusted, so `status` and `doctor` report such a projection as `inactive` and
  name the fix. Reporting it as installed is the false-positive class D189 exists to eliminate.
- **Kiro keeps its unsupported agent and hook cells in the project scope**, for the same D140 reason
  as globally. #248 tracks whether Kiro 3.0 changes that and is blocked on an empirical
  verification; giving it a cell here would be shipping that finding without its evidence.

**Invariants kept.** D169's statement that a projection is **not an authorization boundary** — a
process running as the same user can read any file on the machine; what this prevents is accidental
exposure and activation, and that is all it claims. The signature and trust chain applies per
projection unchanged, the `safepath.go` symlink refusals hold for project-local destinations, and
the bundled transversal skills keep working with no workspace binding at all. A provider in the
default scope resolves to exactly one projection whose behaviour is byte-identical to before.

**Consequences.** New opt-in scope; existing installations are unchanged until someone binds a
workspace. The lockfile gains a `workspaces` key, absent in every file written before this and
therefore needing no migration. Release-note the `workspace` subcommand and `connect --workspace`.

**What the acceptance scenario found.** `18_workspace_projection` is the mandatory E2E from the
plan, and it failed on its first run with three defects no unit test could see: the bundled skills
were being copied into every workspace, a `CLAUDE.md` Cartographer created itself was left untracked
and dirtying the repository, and unbinding a workspace left its projected files behind. All three
lived in the wiring between components, which is exactly what the scenario exists to exercise.

Details: `docs/sync.md` §Workspace scope, `docs/configurator.md`, `docs/interoperability.md`
§Project-local scopes, `docs/testing.md`.

---

<a id="d195"></a>
## D195 — Kiro receives subagents; its hooks are documented but not shipped

**Status: implemented.** Closes #248.

**Context.** D140 settled Kiro's empty `agent` and `hook` cells against **CLI 2.20.0**, on three
findings: the trigger set was camelCase, `~/.kiro/hooks/` played no role because hooks were a `hooks`
map inside an agent config, and hooks were per agent — so no hook Cartographer owns could fire for
the agent a user actually runs. Kiro's documentation for CLI 3.0 claims all three changed:
standalone `.kiro/hooks/*.json` with a versioned schema, `~/.kiro/hooks/` firing in every workspace,
and any custom agent invocable as a sub-agent.

The plan made empirical verification a precondition rather than a formality, because D140's own
history is the argument. That verification was performed on **Kiro CLI 2.21.3**, 2026-09-11, and it
split the plan in two: one half is true and shipped here, the other is not true of any released
client.

**What was verified.**

*Subagents — true, and available today.* `~/.kiro/agents/<name>.json` is reported by
`kiro-cli agent list` as `Global`, and the configuration `kiro-cli agent create -f kiro_default`
writes documents a "Subagent System" with a `use_subagent` tool that delegates to agents **selected
by their `description`**. D140's third finding no longer holds. The format is **JSON**
(`name`, `description`, `prompt`), not the Markdown the vendor documentation describes: a Markdown
agent dropped in the same directory is not discovered.

*Hooks — not implemented in the shipped client.* A hook placed in `~/.kiro/hooks/` **and** in the
workspace's `.kiro/hooks/`, with each of `AgentSpawn`, `SessionStart`, `PromptSubmit`,
`UserPromptSubmit` and `PreToolUse`, never fired — in a `--v3` session that completed normally. The
agent log never mentions hooks, the generated agent config has no `hooks` key, and the shipped agent
binary contains no `.kiro/hooks` string at all (it does contain `.kiro/agents`, `.kiro/skills`,
`.kiro/steering`, `.kiro/settings`). The vendor's own text explains it: the v1 hook format was
*"introduced in IDE 1.0 and CLI 3.0"*, and the CLI changelog puts **3.0 in early access** with
2.21.x on the release channel. `kiro-cli --v3` launches the next-generation *agent* (KAS 0.60.10);
that is not CLI 3.0.

**Decisions.**

- **`agent × kiro` becomes `perName(".json", ".kiro", "agents")`**, in both the global and the
  workspace scope (D193). `translateAgentForKiro` emits JSON with `name`, `description` and
  `prompt`; `tools` and `model` are dropped, as they are for Codex and Antigravity, because their
  names are not portable and inventing a mapping would hand an agent capabilities its author never
  granted. Encoding as JSON rather than concatenating text is what makes a description containing a
  colon or a quote safe.
- **The format is taken from the client, not from the documentation.** Where the two disagree, the
  client is what the user runs. The comment next to the cell records how it was established, so the
  next person can re-run the check instead of re-deriving the conclusion.
- **The provenance block (D138) travels inside `prompt`**, exactly as it does inside Codex's
  `developer_instructions`: a JSON file has no comment syntax, and the prompt is the only field that
  carries free text.
- **`hook × kiro` stays `unsupported`, and the scheduled timer stays its trigger.** Writing a v1
  hook file and reporting it as installed would reproduce precisely the silent no-op D140 refused to
  ship and the false-positive class D189 exists to eliminate. D140's conclusion about the *trigger*
  is therefore not withdrawn — it remains the answer for Hermes, for Antigravity (D194) and for
  Kiro.
- **No version probe, and no `--v3` invitation.** The plan specified
  `kiroGlobalHooksActive(version) bool`, true for major ≥ 3. On the release channel that helper
  would return false on every machine, because the version string stays `2.21.3` with and without
  `--v3` — the version is not the discriminator, and an invitation built on it would state a false
  reason. Nothing here needs one: subagents work on the shipped client, and hooks work on none.
- **`installedSubagentSentence` needed no change.** It derives the sentence from `destDir`, so
  giving Kiro a cell makes the sentence appear on its own — which is what D154 designed it for. The
  comment naming "Kiro and Hermes" as the providers without a subagent directory was corrected to
  Hermes alone.

**Invariants kept.** `skill × kiro` and `instructions × kiro` are untouched — they worked before and
work now. `kiroFlatNamespaceWarning` (D102) concerns the flat **MCP tool** namespace and is
orthogonal. `hookMechanism.noSessionStartEvent` and `SupportsSessionHook` are unchanged, so Kiro
keeps getting the sync-timer advice it needs.

**Consequences.** Kiro clients start receiving subagents they did not have, and a `connect`/`sync`
writes into `~/.kiro/agents/` for the first time — a behaviour change worth a release note. Nothing
about hooks changes. A test that used Kiro as its example of a provider with no `agent` destination
now uses Hermes, which is the last one left.

**Re-check when CLI 3.0 ships.** The hook half of the original plan is not refuted in principle,
only against every released client. It is tracked separately rather than left open here, so this
decision records what was true when it was made.

Details: `docs/interoperability.md` §Kiro hooks, `docs/sync.md` §Kind × provider matrix,
`docs/configurator.md`.
