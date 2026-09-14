---
topic: client-configurator
---

# D194 — Support Google Antigravity in multi-provider client and provisioning

`antigravity` is added as a supported provider in the client configurator and provisioning
engine (D137). It configures Google Antigravity (`agy`) across client discovery, MCP server
emission, standing instructions, skills, subagents, and native hooks.

**Context.** Antigravity is an agentic coding assistant that communicates with MCP servers
and loads project/global instructions and skills. It was previously unsupported by
Cartographer, requiring manual configuration of `.gemini/config/mcp_config.json`,
standing instructions in `.gemini/GEMINI.md`, and skills under `.gemini/config/skills/`.

Every destination below was verified against Google's own documentation rather than inferred:
MCP (<https://antigravity.google/docs/cli/mcp/>), hooks and their five events
(<https://antigravity.google/docs/hooks/>), skills (<https://antigravity.google/docs/skills/>),
subagents (<https://antigravity.google/docs/subagents/>) and global rules
(<https://antigravity.google/docs/rules-workflows/>). These are external contracts that change
outside this project's release cycle: re-verify before editing the matrix.

**Decision.**
- **Registry and Detection (D137).** Registered `ProviderAntigravity Provider = "antigravity"`
  with `DisplayName: "Antigravity"`. Detection probes the Antigravity CLI executable `agy`, the
  Antigravity app bundle on macOS, and Antigravity-specific configuration directories. The legacy
  Gemini CLI executable `gemini` and a generic leftover `.gemini/` directory are deliberately not
  evidence: they would produce false positives after the two products diverged.
- **MCP Configuration Emission.** Emits `.gemini/config/mcp_config.json` with top-level key
  `mcpServers`. HTTP transport emits `serverUrl` and optional `headers`. Header values (such as
  `Bearer ${CARTOGRAPHER_TOKENS}`) are passed through verbatim: Antigravity natively resolves
  `${VAR}` environment variables. Stdio transport emits `command`, `args`, and `env`.
  `DeletableWhenEmpty: true` allows pruning the configuration file and its parent directory
  if removing Cartographer leaves no foreign servers behind.
- **A file Cartographer cannot parse is refused, never normalized.** An earlier revision of this
  work accepted JSONC (line/block comments, trailing commas) in Antigravity's own files by
  stripping them lexically before decoding. Rejected: the rewrite path serializes with
  `json.MarshalIndent`, so every comment the user wrote would be deleted on the first `sync` —
  silently, and on a file Cartographer does not own. Antigravity's documented format is plain
  JSON; the contract is therefore the same one every other provider has had since D1
  ("existing file %s is not valid JSON — fix or delete it manually"), and the file is left
  byte-for-byte untouched when it cannot be parsed. A uniform refusal is better than a
  provider-specific lossy repair.
- **Provisioning Destination Matrix.**
  - `mcp`: `.gemini/config/mcp_config.json` (shared JSON configuration, per-server entries)
  - `instructions`: `.gemini/GEMINI.md` (managed marker-delimited instructions block)
  - `skill`: `.gemini/config/skills/<name>/` (directory materialized with `SKILL.md` and assets)
  - `agent`: `.gemini/config/agents/<name>.md`, translated to Antigravity frontmatter (`name`,
    `description`, `mainAgent: false`, `subagent: true`) while preserving the prompt body
  - `hook`: files in `.gemini/config/hooks/<name>/`, registered as an owned
    `cartographer-<name>` definition in `.gemini/config/hooks.json`; supported native events are
    `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, and `Stop`
- **Trigger semantics, declared as a property and not as a provider check.** Antigravity has
  native hooks but no session-start event: its five events all fire per tool call or per model
  invocation, so mapping `SessionStart` onto one of them would run `sync` on every turn — a
  different behaviour wearing the same name. The hook mechanism therefore declares
  `noSessionStartEvent`, and `SupportsSessionHook` derives the answer from it. KB hooks are
  installed and registered normally; the generated bootstrap hook (D60) is not installed, and
  `connect`/`status` name the scheduled timer (D140) exactly as they do for the other providers
  without a session hook. The property is deliberately data on the mechanism rather than an
  `if provider == …`: the next provider whose hook engine lacks the event declares it too.
- **Mount Point Preservation.** `.gemini` is registered in `provisioningRootDirs` so
  `pruneEmptyDirs` never deletes the user's root Antigravity configuration directory on disconnect
  or skill pruning.

**Known divergence, reported and not repaired.** `~/.gemini/GEMINI.md` is not exclusively
Antigravity's: Gemini CLI writes its own global context to the same path
(<https://github.com/google-gemini/gemini-cli/issues/16058>), and Antigravity additionally reads a
cross-tool `~/.gemini/AGENTS.md`. Cartographer's managed block is marker-delimited and
non-destructive, so coexistence in the file is safe, but the precedence between the two files is
the provider's, not ours. This is the same class of problem as D189 (an instructions file written
correctly and never read): it belongs to the shadowing detection that plan introduces, and is
recorded here so that work covers Antigravity from the start rather than discovering it later.

**Consequences.**
- `cartographer agents` detects installed Antigravity instances.
- `cartographer connect antigravity`, `connect all`, and the interactive connect form configure Antigravity.
- `cartographer disconnect antigravity` prunes managed artifacts and removes MCP entries without
  residue, leaving the root `.gemini/` directory intact.
- `cartographer status` and `sync` track drift and synchronize skills, subagents, hooks, MCP endpoints, and instructions.
- An Antigravity config carrying comments now fails `connect`/`sync` with a named error instead of
  being rewritten. This is the intended behaviour, and the message says what to do.
- Field testing found two incompatibilities after release (#273): the server refused Antigravity's
  notifications over their `Accept` header ([D200](D200-a-post-s-accept-header-is-supplied-not-enforced.md)), and Antigravity drops tools
  whose qualified identifier exceeds 64 characters ([D201](D201-a-provider-s-tool-identifier-limit-is-checked-at.md)).
