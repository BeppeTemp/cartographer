# Multi-provider client

The `cartographer` binary bundles, besides the server (`serve`), the client subcommands that
connect a machine to a Cartographer server: `agents` (discovery), `connect`
(configures + materializes provisioning artifacts — skills, agents, hooks, instructions,
mcp — D69), `disconnect` (disconnects, the inverse of `connect`), `status` (drift, with per-kind
counts), `sync` (realigns), `resolve` (resolves a `{{repo:}}`/`{{path:}}` placeholder, D75), plus
a **TUI dashboard** when invoked with no arguments in a terminal.

The client **always talks to the server over HTTP** (the `sync_pull` tool): there is no
stdio transport on the client side, nor a separate binary — see
[the client decision records](decisions.md#client-configurator) for the
rationale. Generating the MCP config files (`internal/configurator`) and the
materialization logic (`internal/provisioning`) are the same used server-side by
`sync_check`/`sync_apply` (`docs/sync.md`).

## Subcommands

### Discovery, version and output formats

Root help groups commands into Get started, Client, Server, Knowledge base and
Diagnostics. `cartographer version` and `cartographer --version` are aliases;
the help of root, `paths`, `kb`, `client` and `workspace` (`help`, `-h` or
`--help` as the only argument) and of `service` (`-h` or `--help` only) writes to
stdout and exits 0, with no client config needed; no argument outside a terminal
prints the usage on stdout and exits 0, and an unknown subcommand fails on
stderr with exit 2.
Unknown commands only offer a correction for one command within edit distance
two.

`agents`, `status`, and `service status` accept `--output table|json` (table is
the default). JSON is written only to stdout and uses schema
`cartographer.status/v1`: it includes the schema version, effective server URL,
client/server facts, provider states and artifact counts; when relevant it also
contains native-service facts. State and error `code` fields are stable for
automation; a wrapped low-level `cause` is retained in JSON only. `status`
keeps exit 0 for in-sync, 1 for drift and 2 for configuration or operational
errors. `service status` retains 0 running, 3 stopped (on every platform, a Windows task left `Disabled` or `Ready` by `stop` included) and 4 not installed.

### `cartographer hook write-findings [--channel stderr|context|copilot]` (internal)

Not in the usage text. The logic of the client-generated `cartographer-write-findings` hook
([D353](decisions/D353-write-findings-hook.md), `docs/sync.md` §Layer 1): reads a Claude Code, Codex, Copilot CLI or OpenCode 2.x
`PostToolUse` payload on stdin and looks in `tool_response` (the result object, a content-block
array, or JSON in a `text` block, or a string of several JSON values as OpenCode joins them; `supersede` appends `findings:` as plain text) for a `findings`
array or `results[].findings` (`concept_batch`). With at least one finding it writes a fixed English
message (a header with the count, up to 10 `<check> <path>: <message>` lines, `+N more`, and the
instruction to fix them: nothing accepts a finding, D907). `--channel stderr` (default, Claude Code) writes it to stderr
and exits 2; `--channel context` (Codex, [D361](decisions/D361-codex-write-findings-context.md))
prints `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"<message>"}}` on
stdout and exits 0, never 2; `--channel copilot` (Copilot CLI, D676) prints
`{"additionalContext":"<message>"}` on stdout and exits 0, reading Copilot's camelCase payload
(`toolName`, `toolResult.textResultForLlm`, the tool named `<server>-<tool>`). Anything else (no findings, an unknown shape, a parse error, a panic,
an unknown flag or channel) is silent exit 0; with the stderr channel stdout is always empty. A wrong
subcommand name exits 2. `tool_name` may be `mcp__<server>__<tool>`, `<server>_<tool>` (OpenCode, [D362](decisions/D362-opencode-write-findings-plugin.md)), `<server>-<tool>` (Copilot) or the bare tool name.

### `cartographer update check|notice`

Tells an agent, and its user, that a newer release exists
([D254](decisions/D254-agents-are-told-when-cartographer-is-out-of-date.md)). The source is the
GitHub release list (`/releases?per_page=10`, which includes the pre-releases every 0.x tag is;
the newest one that is not a draft and has a version tag wins),
cached for 24 h in `<user cache dir>/cartographer/update-check.json`. A lookup times out after 3 s
and any failure means "unknown": nothing is printed. A `dev` build never checks.
`CARTOGRAPHER_NO_UPDATE_CHECK=1` or `update_check: false` (below) turns it off;
`CARTOGRAPHER_UPDATE_API_URL` exists for tests only.

| Command | Effect |
|---|---|
| `update check [--output table\|json]` | Refreshes regardless of the cache age, prints current, latest, channel and command. Always exit 0 (2 on usage errors). JSON: `{current, latest, available, kind, channel, command}`, additive |
| `update notice` | What the session-start hook runs. Prints **nothing** unless an update exists, then one paragraph for the agent: the versions, the upgrade command, and the instruction to tell the user once and run it only on consent. Always exit 0 |

The command depends on the channel the running binary came from: `homebrew` (Caskroom, or the
`/opt/homebrew/bin`/`/usr/local/bin` symlink into it) → `brew upgrade --cask beppetemp/tap/cartographer`;
`install.sh` (`/usr/local/bin` or `~/.local/bin` as a plain file, or `CARTOGRAPHER_INSTALL_DIR`) →
`curl … install.sh | sh -s -- update`; `install.ps1` (`%LOCALAPPDATA%\Cartographer\bin`) → the
`install.ps1 … update` one-liner; `go-install` (`GOBIN`/`GOPATH/bin`) →
`go install …@<latest>`. `container` (`/.dockerenv`) and `unknown` get no command: the notice
names the image tag or the releases page.

The client **never installs a release by itself** (D906): there is no auto-patch, only this notice and the
command above. A file that still says `update.policy: auto-patch` loads with a warning, and the
realignment turns it into `update_check: true`.

### `cartographer setup`

The first-run path in one command (D253): the native service, the first KB and the agent
clients, then a health check. It adds no behaviour of its own — each step is the existing command
(`service install`/`start`, `kb create`/`kb clone` with `--restart`, `connect --no-input`) — and it
decides every step **before** running any:

| Step | Decided from | Already done when |
|---|---|---|
| server | `service status` | installed and running (installed but stopped → `service start`) |
| KB | `--remote`, `--no-remote`, or the interview; `git ls-remote <url>` | a KB in the data dir has that origin (trailing `/` and `.git` ignored) |
| auth check | an unauthenticated `GET /mcp` on the service (skipped when the client config already sends a token) | answers anything but 401 |
| agents | `--agents`, else every detected client | never skipped: `connect` is idempotent and re-syncs |
| verify | `/health` through `service status` | — |

- **The remote decides create vs clone.** An empty `ls-remote` answer means an empty repository:
  `kb create <name> --remote <url>`; any ref means content: `kb clone <url> <name>` — which, for a repository holding only a forge's initial commit (README/LICENSE/.gitignore), answers with `kb create <name> --remote <url>` instead, since that adopts it (D710). A README.md of that commit is replaced by a short generated one (a new commit on top, the forge's commit stays) only when it is a known forge boilerplate (the title and at most a description line of GitHub/Gitea/Forgejo, GitLab's "Getting started" template); any other README is kept and a hint names `--readme generate`, which replaces it whatever it holds, while `--readme keep` never replaces it (D1071). `kb create --branch X` (with `--remote`) makes `X` the KB's branch: it writes `branch: X` in `settings.yaml`, pushes the default branch carrying it, then creates and pushes `X` and leaves the clone on it; `kb clone --branch X` is per server instead: it checks `X` out and adds a `kbs[]` entry with `branch: X` to the server config (D1093). The name is
  `--name`, else the repository name. The same probe proves the remote is reachable with this
  machine's credentials, under the non-interactive git environment of `kb clone` (no prompts,
  SSH batch mode, host keys never auto-accepted — D173); a failure exits 2 **before anything is
  written**, with the remedy for the recognised git errors.
- **Workspace.** `--workspace <repo>` is passed through to `connect --workspace` (D193): the KBs are
  confined to that repository instead of every directory.
- **KB binding (D190).** `--kb` is passed through. Without it: one KB binds itself; agents that
  already carry a binding keep it; a KB this run adds next to existing ones is bound alone (the
  narrowest choice); otherwise, with two or more KBs and nothing to go on, an interactive run
  asks with the same picker as `connect` and a non-interactive one exits 2 naming `--kb`.
- **Refusals, all before any change:** no `git` on `PATH`; a client already pointed at a
  non-loopback server (setup provisions a local one — `connect` is the command for a remote
  server); no remote and no mounted KB in a non-interactive run; no agent detected and none named.
- **A service that demands a token stops setup before `connect` (D268).** The agents setup
  configures reach the local server with no token, so a service answering 401 would fail them one
  by one (`sync_pull: … unauthorized (401)`). That happens when a `CARTOGRAPHER_TOKENS` exported
  for another server reaches the service's environment under a `server.yaml` without `auth.mode`
  under a `server.yaml` that does not pin it; a config `service install` generates pins `mode: "off"` for a
  loopback address (D268). The error names the variable (saying whether it is set in the current shell,
  and `CARTOGRAPHER_AUTH` if that forces auth on), the config file to add `auth: {mode: "off"}`
  to, and `service restart`; exit 1, and the rerun resumes at the agents.
- **Interactive** (a TTY, no `--no-input`): asks for the remote when none is mounted — blank means
  a local-only KB, confirmed explicitly (D134) — then which of the detected agents to connect, prints
  the plan and asks `Proceed? [Y/n]` (skipped by `--yes`). `--dry-run` prints the plan and exits 0.
- **Exit codes:** 0 set up; 1 a step failed or the operator cancelled; 2 usage, preflight or
  choice error (nothing changed). A failed step prints `Setup stopped at …`; a rerun skips what is
  done. On Linux a freshly installed user unit without lingering gets the `loginctl enable-linger`
  hint. The run ends on the Atlas URL and on restarting the agent sessions.

```bash
cartographer setup                                                  # interview, plan, confirm
cartographer setup --remote git@github.com:me/wiki.git --agents claude --yes --no-input
cartographer setup --no-remote --name trial --agents codex --yes    # local-only KB
cartographer setup --remote <url> --dry-run                          # the plan, nothing changed
```

The install scripts end a first install on `Next: cartographer setup`. The Homebrew cask cannot
tell a first install from an upgrade, and its caveat names both: `First install: cartographer
setup`, and for an upgrade that the next `cartographer sync` switches the service to the new binary
or `cartographer upgrade-repair` does it now (D280). The dashboard shows the same hint on its `next` line while a loopback server has no service
behind it or answers with no KB mounted.

### `cartographer agents`

Lists the supported providers, whether they are installed on the machine and whether they are
connected (recorded in the client state file `.cartographer-state.json`, beside the machine-wide `~/.cartographer.yaml`).

`internal/agents.Detect` probes, in this order, and stops at the first match; the heuristic that
matched is reported in the DETECTION column, and as `detected_by` in JSON
([D224](decisions/D224-detection-records-which-heuristic-matched.md)):

1. `binary` — any of the provider's binaries in `PATH` (Kiro ships as `kiro` from the IDE and `kiro-cli`
   standalone). On Windows `exec.LookPath` honours `PATHEXT`, so this is where a client installed
   normally is found;
2. `config-dir` — a known configuration directory under the home directory;
3. `env-config-dir` — a configuration directory anchored at an environment variable, for a location no home-relative
   path can express — today `%APPDATA%\opencode`. An entry whose variable is unset is skipped, so
   declaring one costs nothing on a platform that does not define it;
4. `app-dir` — an application-installation directory declared for this GOOS. Only a confirmed location is
   declared: `/Applications/Kiro.app` and `/Applications/Antigravity.app` on darwin, nothing on
   Windows, where the CLI on `PATH` is the detection that matters and an unverified install path
   would only invent evidence ([D216](decisions/D216-the-client-half-reaches-parity-on-windows.md)).

```bash
cartographer agents
```

```
PROVIDER   INSTALLED  CONNECTED  DETECTION   EVIDENCE
claude     yes        no         config-dir  /Users/u/.claude
codex      yes        yes        binary      /opt/homebrew/bin/codex
kiro       no         no         -           -
```

The distinction the column carries is the one `INSTALLED` cannot: `binary` is a client that is
on the machine now, while `config-dir`, `env-config-dir` and `app-dir` are
directories, which a client that was removed leaves behind. `INSTALLED` — and therefore which
providers a bare `connect` or `connect all` targets — is unchanged: it is still true for any
match ([#305](https://github.com/BeppeTemp/cartographer/issues/305)).

### `cartographer connect [provider|all]`

Generates the MCP config (HTTP transport only), materializes artifacts via `sync_pull` (the full
kind×provider matrix is in `sync.md` §Kind × provider matrix; combinations with no known
destination go to `unsupported` and are filtered upstream), and writes/updates `.cartographer.yaml`. If the
server is unreachable, the MCP configs and `.cartographer.yaml` are still written; materialization
is **deferred** (warning, exit 0) — it must be completed with `cartographer sync` once the
server is up. Materialized hooks are also **automatically registered** in the
provider's native mechanism (`settings.json` / `config.toml` / JS plugin — `sync.md` §Agents and
hooks); `connect`/`sync` print an info line for each one. `connect`, `sync` and `upgrade-repair` also remove the
scheduled headless doctor job an earlier release installed, printing one line when there was one:
Cartographer never starts an agent by itself (D704). `sync` matters after a Homebrew upgrade, which
runs neither of the others; `doctor run|schedule|unschedule|status` fail as unknown arguments.

**Token file (D699).** With `auth: true` and a `token: ${NAME}` reference (the default is `${CARTOGRAPHER_TOKENS}`),
`connect` (so `reconnect` and `setup`) also copies the token to a `.cartographer-token` file next to `.cartographer.yaml` (`0600`, atomic, never through a symlink), because the sync
timer runs without a login shell's exports. The environment variable stays the source
whenever it is set; rotate it and run `cartographer reconnect` to refresh the copy. A file readable by group or
others is refused (`chmod 600` fixes it). `disconnect` removes it with the last provider; `reconnect` keeps it.
The file is what an unattended run reads when the variable is absent. A **literal** `token: <value>` needs
no copy and no variable: `.cartographer.yaml` is then written with mode `0600` and `connect` writes no
token file; the agents' MCP entries can only name a variable, so they reference `$CARTOGRAPHER_TOKENS`
and never carry the literal (nor does any provider file). A literal token therefore reaches the agents only if
`$CARTOGRAPHER_TOKENS` is exported in the environment that starts them, and `connect` and `sync` print a warning
saying so; for agents that should work without it, use a `${NAME}` reference. A token is never put on a command line (`--token-env` takes a variable name,
no flag takes a value) and never printed.

**Multi-KB servers (D92).** `connect` reads `GET /health`, with the token, before emitting MCP
configuration (a scoped token sees only the KBs it reads, D1096). With one mounted KB (or an older single-KB server that omits
`kbs`) it keeps the compatible single entry, `<server_name>`, pointed at the
bare `/mcp` URL. With two or more KBs it writes one entry per KB, named
`<server_name>-<kb>` and pointed at `/mcp?kb=<kb>`, and records that KB list in
`.cartographer-state.json` (`known_kbs`). `sync` repeats the enumeration: it adds new
entries, removes entries for disappeared KBs, and performs the bare↔suffixed
rename on one-to-many transitions. If the server cannot be reached, it leaves
the MCP entries and `known_kbs` untouched and warns; run `sync` again once it is
up.

**Kiro subagents (D195).** Kiro receives KB subagents as JSON configs in
`~/.kiro/agents/` (and `.kiro/agents/` in workspace scope), which `kiro-cli agent
list` reports as Global/Workspace and the built-in agent delegates to through its
`use_subagent` tool, selecting by `description`.

**Kiro hooks (D300).** KB hooks and the bootstrap hook reach Kiro as entries of
`~/.kiro/hooks/cartographer.json`, a standalone hook file Cartographer owns whole,
with their files in `~/.kiro/hooks/cartographer/<name>/`. They fire only in
`kiro-cli chat --v3 --tui`, so `status` reports `hook n/m (fires in kiro-cli chat --v3
--tui only)`. `connect`, `status` and `doctor` keep advising the scheduled timer for
Kiro, naming that limit rather than calling it hookless; `status` stops repeating it once
`connect` or `reconnect` has shown it (D320, `sync.md` §Detection and triggers). Nothing is written into a
Kiro agent config and `chat.defaultAgent` is never set (`interoperability.md` §Kiro
hooks).

**Workspace scope (D193).** `cartographer workspace bind <provider> <path> --kb <name>…` moves a
provider from one machine-wide catalogue to one projection per bound repository: the KBs land in
that repository's own project-local directories and nothing KB-sourced is written under `$HOME`. `connect --workspace <repo>` makes the same choice at connect time, which is the only moment
it is free — a provider-global connect materializes every selected KB into `$HOME` first, and moving
them afterwards is a migration. `workspace unbind` removes the declaration and the next `sync`
prunes what was projected there. A binding also scopes the **tools** in every client, with no file
written into the workspace: `sync` writes the provider's global MCP entry as a local stdio bridge,
`cartographer mcp --provider <p> [--dir <folder>]` (the folder defaults to the working directory), which serves only the KBs of the folder the session runs in
(deepest binding wins; an unbound folder, `$HOME` included, gets the provider's default set
**read-only** and no KB artifact (D1018); `--no-kb` means no KB, never every KB). It is a scope filter, not a security boundary: hard isolation needs a per-workspace
token (D44/D45). Artifacts stay opt-in per scope (above). `workspace resolve [<path>] [--provider <p>]` shows which
binding wins for a folder (for one provider, or each), says when it is derived from a KB's `paths.yaml` (D1019), and prints `read-only` for one that no binding covers. `workspace list` shows the bindings, with `[gone]` next to a
directory that is not there any more and `[derived: paths.yaml of kb-a]` next to one a KB's declared repository implies, then the declared repositories with no local clone (D737, `sync.md` §Workspace scope). Limits: the bridge opens no
server-push stream; if `cartographer` is not on `PATH` when `sync` runs, the provider keeps its HTTP
entry with a warning; a client with `roots` in an unbound folder gets a handshake naming no KB.

Existing configurations are untouched: the scope is per provider, absent means the historical global
catalogue, and no upgrade changes it. Full rules, the project-local destination matrix, the
repository-hygiene guarantees and the providers that cannot be scoped at all → `sync.md`
§Workspace scope.

**One routed entry (D187, D288).** When `/health` reports `routed_path` (every server since D288),
the KB is no longer part of the entry name: the client writes **one** entry per provider,
`<server_name>`, pointed at `/mcp/routed`. The provider's binding travels in the URL: an explicit
binding writes `?kbs=<bound KBs, sorted, comma-joined>`, which the server turns into that connection's
KB set (two or more KBs: `kb` is a required enum in every schema; exactly one: no `kb` argument at
all); the default binding, every KB the server mounts, writes no `kbs`, so a KB added later reaches the
provider without a rewrite. Every routed entry ends with `provisioned=1` (`/mcp/routed?provisioned=1`,
`/mcp/routed?kbs=kb-a,work-kb&provisioned=1`; D1097): the client has the managed block, so the server sends
it no operating guide in `initialize`. The marker is always written, a server that predates it ignores it,
and `mcpbridge` adds it to the URL it forwards to. One `sync` adds it to an entry written before. A bound name the server no longer mounts is left out of the URL (the server
refuses an unknown name), and a provider bound only to vanished KBs gets no entry, the same declaration
as an explicit empty binding. `kbs` only narrows, never widens: the per-KB authorization still runs at
the target, and with `auth: false` the binding is advisory, since the client can edit its own URL.
The generated instructions block names the bare tools and says to pass `kb: "<name>"` where a tool
takes a `kb` argument, which is true for both shapes. The topology is persisted in
`.cartographer-state.json` (`server_mount_mode`, `server_routed_path`) so `doctor` and `status` can derive
the expected entries offline.

**`sync` heals a topology change; no reconnect is needed.** A client whose state predates D288 holds
per-KB entries (`<server_name>-<kb> → /mcp?kb=<kb>`). The next `cartographer sync` removes every name the
client may have owned (the bare name and one per known KB, including unbound ones) and writes the single
routed entry, so one sync leaves exactly one entry per provider and no orphan. `cartographer status`
reports `mount mode changed: …` and names `cartographer sync` until then. Against a server that does
**not** report `routed_path` (pre-D288) the client keeps the historical per-KB shape, with the two
warnings below.

**Kiro and flat tool namespaces (D102), pre-D288 servers only.** Kiro's MCP tool namespace is flat
across servers: writing 2+ MCP entries for `kiro` against a server that does not route leaves only one
KB's tools reachable in a Kiro session unless that server mounts the others with a `tool_prefix`.
`connect`/`sync` warn on stderr in that case and read each KB's effective `tool_prefix` from `/health`.
Against a D288 server the warning never fires: one entry cannot collide with itself.

**Antigravity and the 64-character tool identifier (D201), pre-D288 servers only.** Antigravity shows
each tool as `mcp_<server>_<tool>` and drops any identifier over 64 characters, silently.
`connect`/`sync` compute the longest identifier each Antigravity entry would produce (entry name, the
KB's effective `tool_prefix` from `/health`, the longest tool name) and warn on stderr when it exceeds
the limit. A routed entry carries unprefixed tools and is checked with no prefix; without `/health`
facts the check stays silent.

**Prefix discovery (D120), pre-D288 servers only.** Every client-owned direct tool call (manifest pull
during `sync`, remote `reindex`, the TUI's status probes) qualifies the tool name with the prefix the
server advertises for that KB in `/health`, never one re-derived locally from the KB name. A D288 server
advertises none, so the tool names are bare.

```bash
cartographer connect                                   # all agents detected on the machine
cartographer connect claude                             # Claude Code only
cartographer connect --agents claude,codex              # selected subset
cartographer connect opencode --server-url http://cartographer.example.com/mcp --auth
cartographer connect claude --pin-key homelab=0123...  # pin a KB Ed25519 public key
cartographer connect all --dry-run
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `antigravity` \| `crush` \| `copilot` \| `all` (all detected agents) |
| `--agents` | *(unset)* | Comma-separated subset (`claude,codex`); cannot be combined with the positional provider |
| `--kb` | *(unset)* | Which KBs this client may receive (repeatable, or comma-separated; `all` for every mounted KB). Required on a **first** connect against a server mounting two or more KBs — see below (D190) |
| `--server-url` | `http://127.0.0.1:39273/mcp` | Cartographer server URL |
| `--auth` | `false` | Enables the Bearer header in generated configs |
| `--token-env` | `CARTOGRAPHER_TOKENS` | Name of the env var holding the Bearer token; written as `token: ${NAME}` |
| `--dry-run` | `false` | Prints what would be written, in the conditional (`would write`, `would connect`), and writes nothing ([D147](decisions/D147-every-reported-write-is-observed-never-intended.md)) |
| `--allow-insecure-http` | `false` | Accepts a plain `http://` server URL that is not loopback and records `allow_insecure_http: true`; without it `connect` refuses such a URL (§Transport security, D1023) |
| `--pin-key` | *(repeatable)* | Pins `KB=PUBLIC_KEY` for Ed25519-verified provisioning artifacts; existing pins are preserved |
| `--no-input` | `false` | Never opens the interactive form, even in a TTY |
| `--workspace` | *(unset)* | Projects the selected KBs into this repository instead of the global catalogue (D193) |
| `--auto-trust` | `false` | Accepted and ignored, with a warning (D906) |

### Transport security (D1023)

The bearer token and the responses must not cross the network in clear. Every
client request goes through `client.CheckServerURL`: `https://` is always
accepted; `http://` only when the host is loopback (`localhost`, or an IP for
which `net.ParseIP(host).IsLoopback()` holds: `127.0.0.0/8`, `::1`, IPv4-mapped
`::ffff:127.0.0.1`; a name such as `127.0.0.1.example.com` is not); any other
scheme is refused. The check is in the client transport, so `connect`, `sync`,
`status`, `doctor`, `kb repair`, `kb reauthor`, `reindex`, the usage sync and the stdio bridge
all obey it, and `connect` and `sync` fail before writing anything.

The explicit opt-in is the `allow_insecure_http: true` key, set by
`connect --allow-insecure-http` (re-run `connect` with the same flags; it only
adds the key), or `CARTOGRAPHER_ALLOW_INSECURE_HTTP=1` for one run. A machine
already connected to a plain-http remote is refused at its next `sync` with an
error that names both fixes. Nothing upgrades `http` to `https` by itself.

`signing_keys` in `.cartographer.yaml` stores public-key pins per KB. Pins are
operator-supplied and are never learned from `sync_pull`; multiple pins permit
key rotation. Pin the new key, switch the server signer, then remove the old
pin after all clients have synchronized.

If no provider is detected and no explicit name is passed, the command exits with an error
(exit 1) without writing anything.

**Interactive form (TTY, D49+D64+D86).** With no form flags and in a TTY, the form shared with the
TUI opens (`connectform.go`): each field shows a contextual hint below it when focused
("Token env var" is the **name** of the environment variable holding the bearer token — the token
itself is not entered here, the form saves only the name; with Auth off the field is rendered secondary and the hint says it is
ignored). In the standalone `connect` form, one checkbox per provider is pre-selected from the
installed-agent set; select one or more with Space or Enter. The Server URL prefill follows the precedence existing `.cartographer.yaml` >
`CARTOGRAPHER_SERVER_URL` (client env) > `http://127.0.0.1:39273/mcp` — the loopback literal the local service listens on, never `localhost`, which Windows resolves to `::1` first, where nothing listens (D231). A `.cartographer.yaml` still carrying the old default `http://localhost:39273/...` is read back on `127.0.0.1` (path kept), so the next `connect` or `sync` rewrites every client's MCP entry; any other URL is left as written. On submit a **probe** runs
(`client.Health`, `GET /health`, 5s timeout, token from env only if Auth is enabled) before writing
any file: a reachable server with no mounted KB explains the `kb create` then service-restart path;
otherwise on failure the form is re-shown with the entered values and an inline error
(distinguishing a 401 "token rejected" from a network error), with an override available — in CLI a
`y/N` prompt "proceed anyway?", in the TUI a second consecutive Connect with no changes forces the
connection. A failed `doConnect` also re-shows the form populated (connect is idempotent: no
`disconnect` is needed to retry).

**Placeholder step (TTY, D262).** After the first materialization, when some `{{repo:…}}`/`{{path:…}}`
key the bound KBs cite did not resolve on this machine, a TTY `connect` (no `--no-input`, no
`--dry-run`) opens a second step (`pathsform.go`, a separate step like the KB choice because the keys
are only known after the pull): one row per unresolved key, with its KBs, the `description` its KB's
`paths.yaml` declares (D263), the reason, a text input for the local path — pre-filled with the
declared `default` when there is one (it did not resolve, so it does not exist here yet) — and an
empty value to skip it. A key whose declared default exists is resolved without being asked. Answers are written into `paths:` in
`.cartographer.yaml` and the instructions block is materialized once more so its "Local paths" table
carries them. Outside a TTY nothing is asked: the apply summary prints one summary line for the unresolved keys, with detail and the
`cartographer paths set`/`paths ignore` fixes only for keys new since the last sync (D282), and the exit status is
unchanged — an unresolved key never blocks a connect.

**Local service (D73).** If the probe fails, the URL is loopback (`localhost`/`127.0.0.1`/`::1`),
and the native service isn't running, before the `y/N` override the CLI flow offers to
install and start the local service (`cartographer service install` with defaults, polling
`/health` for up to 10s, then an automatic re-probe). In the non-interactive path, a deferred
materialization to a loopback URL only adds a hint on stderr suggesting
`cartographer service install` when unreachable; a reachable 0-KB server instead prints
`cartographer kb create <name> --remote <url>` followed by `cartographer service restart`. A successful connect
prints the absolute paths of generated MCP configs and reminds the user to restart the selected
agent sessions to load the MCP tools.

### `cartographer disconnect [provider|all]`

The inverse of `connect`: for each target provider — default `all` = every provider **connected**
in the client state — it surgically removes every managed MCP server entry (the bare name and
any persisted per-KB suffixed names) from that provider's
config file (`internal/configurator.Remove`, the inverse non-destructive merge: the rest of the
file is left intact; an emptied `mcpServers`/`mcp` map is dropped, and the file itself is deleted only when it is left an empty shell and the provider's registry entry sets `DeletableWhenEmpty`, never `.claude.json`), prunes the
managed artifacts registered for that provider in the lockfile (`provisioning.PruneManaged` — only
managed files, never untracked ones), then removes the provider from the lockfile and from
the client state. If the lockfile ends up with no providers it is removed; `.cartographer.yaml`,
on the other hand, is **never deleted** (D64): with zero agents it stays on disk with an empty agent list in the state file,
preserving `server_url`/`server_name`/`auth`/`token`/`clients` as defaults for the next
`connect` (a disconnect→connect restarts from the previous server, not from `http://127.0.0.1:39273/mcp`).

```bash
cartographer disconnect                # every connected provider
cartographer disconnect claude         # Claude Code only
cartographer disconnect --agents claude,codex # selected subset
cartographer disconnect all --dry-run  # preview without writing
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `antigravity` \| `crush` \| `copilot` \| `all` (every connected provider) |
| `--agents` | *(unset)* | Comma-separated subset (`claude,codex`); cannot be combined with the positional provider |
| `--dry-run` | `false` | Prints without removing |

Idempotent: exit 0 even if there was nothing to remove (no `.cartographer.yaml`, provider
already disconnected, MCP entry already absent, ...). Exit 2 only on an actual error (I/O, malformed
provider config JSON, ...). Exit 1 when a provider was kept connected because its config or managed
files sit behind a symlink (§Symlinked config files and directories are refused); the other providers
are still disconnected.

### `cartographer status`

Compares the server's manifest revision with the last applied lockfile, for every connected
provider. Read-only.

```bash
cartographer status
```

Exit code: `0` all providers in sync, `1` at least one provider in drift, `2` error (no
`.cartographer.yaml`, server unreachable, ...). For every provider it also prints per-kind
counts (`provisioning.KindCounts`), e.g. `skill 4/5 · agent 2/2 · hook 1/1`. On drift it
prints the diff (added/updated/removed, with a `trust` state: `built_in`, `verified`, `trusted`,
`approved`, `approval_stale` or `needs_approval` — see [D115](decisions/D115-mcp-allow-list-and-hash-bound-local-approval.md)
for the MCP-specific approval states; an unsigned KB artifact that is not an MCP descriptor, a hook or an executable file is `trusted`: it is installed,
D906). Artifacts in `needs_approval`/`approval_stale` get their own
`cartographer approve <kind> <name> --kb <kb>` hint. Before the artifact report it prints the
client and server versions. A non-`dev` mismatch is a warning only (it does not change the exit
code); on loopback, an installed local service also gets a `cartographer upgrade-repair` hint
(D121: it replaces the running service **and** re-syncs the providers in place, where the former
`service restart` hint only did the first half).
A newer release known to the update cache adds `update available: vX (installed vY) — <command>`
(JSON `update: {latest, kind, channel, command}`, present only then), and a remote server that
reports one in `/health` adds `server update available: vX` — the server operator's job, so no
command. Both read local state only (the cache is refreshed by the session hook and `update
check`) and neither changes the exit code (D254).
Placeholder keys the last sync could not resolve and the operator did not `paths ignore` (read from the lockfile, no network) add
``N placeholder(s) unresolved — run `cartographer paths` `` to the table and an additive
`unresolved_placeholders: [{key, kbs, reason}]` to the JSON, present only when non-empty; neither
changes the state or the exit code — an unresolved key is left verbatim, not a failed sync (D262).
For an unavailable endpoint, the table names the configured endpoint once and
suggests checking that URL (or `cartographer service status` for loopback);
connected providers are reported as `unknown`, rather than repeating a network
failure for each provider.
A managed artifact that diverged on disk is listed as `diverged on disk (<reason>)`; for an
`unrunnable` hook — a Claude Code hook whose registered command cannot run, such as a backslash
path on Windows or a file that is not there — the next line says which command and why (JSON
`detail`), and `sync` re-registers it (D267).

### Dashboard

With no subcommand in a TTY, the dashboard renders the same status snapshot as
`status`. Its server panel is a labelled block — endpoint with state and
readiness, client/server versions (with ` · vX available` in the drift colour when a newer
release is cached, D254), the local native service when one is
installed, and the KB inventory with, per KB, how many connected providers are
**bound** to it (a count of bindings: not sessions, and not a confirmation that
a sync has run, so `kb-tre (0)` means the server serves it and nothing consumes
it). `Enter` connects a disconnected provider or syncs a connected one; `s`
syncs the selected provider, `S` syncs every connected provider one at a time
and names the one in flight, `d` opens the disconnect confirmation — which
names the KBs whose artifacts will be removed — and `r` refreshes. Unavailable
actions are omitted from the contextual key map. At 60 columns it uses compact
labels and shortened endpoints; 80 is the normal layout and 120 retains full
endpoint and artifact detail. Failures keep the current selection and entered
connect-form values.

### `cartographer sync`

Re-runs `sync_pull` and reapplies the manifest for every connected provider: materializes
add/update, prunes obsolete artifacts, updates the lockfile. Idempotent.

```bash
cartographer sync [--client <provider>]... [--dry-run] [--no-heal] [--log-file <path>] [--report-to <url>]
```

| Flag | Default | Effect |
|---|---|---|
| `--client` | *(all)* | Syncs only this provider (repeatable). A provider left out is not touched at all: not its MCP entries, not its artifacts, not its lockfile entry |
| `--dry-run` | `false` | Prints without writing |
| `--no-heal` | `false` | Reports managed artifacts that diverged on disk instead of restoring them from the server (D139) |
| `--log-file` | *(none)* | Appends this run's output to that file instead of stdout/stderr (created if absent, never rotated); the Windows sync task uses it, where nothing else records a failing background run (D217) |
| `--auto-trust` | `false` | Accepted and ignored, with a warning (D906): session-start hooks written by older versions still pass it |
| `--report-to` | *(none)* | Only sends this machine's usage and client-settings reports (D326, D818) to that MCP endpoint, and changes nothing: the config is read, never saved, nothing is provisioned, the configured server is not contacted. It is how `make dev DATA=<dir>` shows the real client on a live instance (D1044). The configured token goes with the reports, so the URL must name a server on this machine (`localhost`, `127.0.0.1`, `::1`): any other host is refused with exit 2 |

For Claude Code the `cartographer` MCP entries are written to `$HOME/.claude.json` and to
`<dir>/.claude.json` for every `clients.claude.config_dirs` (D755), and removed from all of them.
A declared dir that is missing, relative or holds a symlinked `.claude.json` is skipped with a
warning, never created. A `CLAUDE_CONFIG_DIR` set at sync time but not declared is a warning
naming the key; nothing is written there. Skills, agents, hooks, `CLAUDE.md`, the bootstrap hook
and KB-provided `mcp` artifacts stay under `$HOME`.

Each provider receives only the KBs bound to it (§`cartographer client`), so the
manifest — and therefore the revision — differs per provider. When every targeted
provider agrees, `sync` prints one `synced to revision <r>` line as before;
when bindings made them diverge it prints one line per provider instead, rather
than implying an agreement that does not exist. See [`sync.md`](sync.md)
§Per-provider projection.

A KB whose `sync_pull` fails does not stop the others. `sync` applies every
provider whose bound KBs all answered, leaves each provider bound to a failed KB
exactly as it was (its MCP entries, artifacts and lockfile entry, as if `--client`
had left it out), then exits `2` with one error listing each failed KB and the
providers it skipped. When every targeted provider needs a failed KB, nothing is
written and the error says so. `upgrade-repair` gets the same behaviour through
the same code, and reports the provider sync as pending
([D257](decisions/D257-sync-continues-past-a-kb-that-cannot-be-pulled.md)).

Before anything else — before the client-state lock and before the first network call, `--dry-run`
included — `sync` checks the environment it needs: when `.cartographer.yaml` has `auth: true` with a
`token: ${NAME}` reference, that variable **or the token file** (D699); a literal token needs nothing here. Missing ones are reported in one error naming each, so an
unattended run (session-start hook, sync timer, CI — none of which inherit a login shell's exports)
diagnoses its misconfiguration before the first network call
([D222](decisions/D222-sync-checks-the-environment-once-and-the-401-names.md)). The check is
read-only and never contacts the server: it verifies a token *exists*, not that the server accepts
it — a token that is set but wrong is still a 401 from `sync_pull`. When nothing is missing it
prints nothing. 

Every sync verifies the managed files on disk, not just the manifest revision, and restores what
was edited or deleted locally — the restore is reported on its own line, because it discards
someone's local change. See [`sync.md`](sync.md) §On-disk verification and healing.

This is also how a configured provider is repaired **in place** after a local upgrade: the same
in-process runner is what `cartographer upgrade-repair` calls (D121). Repair in place is the
default; a full rebuild is `cartographer reconnect` (below), for the residues no incremental sync
can see. Already-open provider sessions still need to be restarted to reopen the MCP connection.

When the server that answers is not the one this client's state was materialized against, `sync`
prints one line saying so — once per invocation, whatever the provider count — and then syncs
normally ([D142](decisions/D142-reconnect-rebuild-a-client-configuration-never.md)). The line states
the fact and that this run re-applies the current manifest; it carries no imperative, because the
run it precedes is the repair ([D220](decisions/D220-sync-states-the-server-change-without-recommending.md)).
`status` and `doctor` report the same fact and do name `reconnect`: they only observe. It reports; it
never escalates on its own. An unknown version on either side (a lockfile written before D142, an
unreachable server) and a local `dev` build say nothing.

### `cartographer reconnect [provider|all]`

Rebuilds a provider's configuration from scratch: a full `disconnect` followed by a full `connect`,
in one invocation, reusing both rather than being a third implementation of either
([D142](decisions/D142-reconnect-rebuild-a-client-configuration-never.md)).

```bash
cartographer reconnect                 # every connected provider
cartographer reconnect claude          # Claude Code only
cartographer reconnect --agents claude,codex
cartographer reconnect --dry-run       # preview both halves, write nothing
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `antigravity` \| `crush` \| `copilot` \| `all` (every connected provider) |
| `--agents` | *(unset)* | Comma-separated subset; cannot be combined with the positional provider |
| `--dry-run` | `false` | Both halves simulate, nothing is written |

**When to prefer it over `sync`.** Pruning is managed-only, so anything an *older Cartographer
version* wrote under a different name — a generated plugin whose filename changed, a managed block
whose marker spelling changed, a hook registered outside the block — is not in the current managed
set and survives every sync. The connect half writes the current shape from nothing, which is what
removes them. For everything else, `sync` is the right tool and stays incremental.

**What it preserves.** Everything in `.cartographer.yaml`: server URL and name, auth mode and token
env (the `token` reference), pinned signing keys, MCP approvals, search roots and paths. It is a rebuild, not a
reset — a user who had to re-approve every MCP descriptor after an upgrade would simply stop running
it. A provider that was not previously connected is rebuilt all the same, stating that it was not.

**It is never automatic.** No upgrade path invokes it: the reasoning is D121's ("automatic repair
never invents an approval, never broadens trust"), and removing and rewriting provider configuration
is a bigger hammer than a repair.

**Partial failure.** If the connect half fails after the disconnect half succeeded, the command
exits 2, names the providers now left disconnected, and prints the exact `cartographer connect`
invocation — with the same settings — that finishes the job. Silence there would leave an agent
without its MCP endpoint. A `--dry-run` failure leaves nothing disconnected, so it says nothing.

After a successful rebuild the summary ends with the reminder that **already-open agent sessions
must be restarted** to pick up the rewritten MCP configuration: the one step no client-side command
can perform.

### `cartographer doctor`

Read-only diagnosis of this machine's client configuration
([D143](decisions/D143-doctor-a-separate-command-that-diagnoses-and-never.md)). `status` answers "is the applied revision current";
`doctor` answers the question an operator actually has after an upgrade or a half-finished
migration: *is there anything left over here that should not be, or missing that should?*

```bash
cartographer doctor [--json] [--provider claude]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Emit the report as JSON (`cartographer.doctor/v1`) instead of text |
| `--provider` | *(unset)* | Narrow the run to one provider — inspected even if it is not connected |

**It never repairs, and never writes** — no lockfile migration, no directory creation, no cache
refresh — except `--repair-hashes` below, the one flag that rewrites the lockfile. Every finding names a real path on this machine and the command that fixes it (`sync`,
`reconnect`, `connect`, `service sync-timer install`), because a diagnosis nobody can act on is
noise and a doctor that silently fixes things is a doctor nobody can predict.

The checks:

| Check | What it looks at |
|---|---|
| `client-config` | `.cartographer.yaml` exists and parses; an agent is connected; every configured provider is still installed |
| `lockfile` | present, readable, and in the v2 format — a v1 file on disk is migrated *in memory* by every read, but stays v1 until something rewrites it |
| `managed-files` | the on-disk verification of D139, per provider: `missing`, `modified`, `unregistered`, `unrunnable` (a Claude Code hook whose registered command cannot run, with the reason — D267); plus files sitting inside a managed skill/hook directory that no lock entry accounts for (D178) — reported only, since doctor cannot prove Cartographer wrote them |
| `mcp-entries` | the Cartographer entries in the provider's native config match the KBs recorded in `.cartographer.yaml` — an entry for a KB the server no longer mounts, or a missing one |
| `instructions` | exactly one well-formed managed block per provider that has instructions materialized (begin recognized by prefix, so a block written by an older version still counts), **and** that the provider actually reads the file it was written into (D189) |
| `hooks` | one native registration per managed hook — the D99 double-fire is a registration left outside the managed block by Codex's own rewrite; for OpenCode, the plugin shape written against the installed major (D359) — a 1.x plugin on OpenCode 2.x is an error, an unsupported major a warning, an unreadable version a warning (the shape was guessed, D719) |
| `server` | `/health` reachable; the recorded `server_version` (D142) against the live one; client binary against server. When an unreachable server is loopback **and** no local native service is installed, the finding names that cause and the two remedies instead of pointing at `service status`, which would only repeat `installed: false` (D174) |
| `trigger` | every connected provider has a session hook, or the scheduled trigger is installed (D140); a timer the operator uninstalled on purpose is info (D325) |
| `capability` | every per-KB gate the server advertises on `/health` is on, and no KB was mounted by discovery rather than by a `kbs[]` entry (D151). Info severity: it names the setting that would change it |
| `symlink` | no managed destination directory is a symlink, or anything else that is not a plain directory — provisioning refuses to write through one, so the artifacts it would hold are not installed (D148, widened in D216) |
| `kb-collisions` | no two KBs bound to the same provider claim one `kind`+`name` (D171). `sync` refuses outright when they do, so a machine that has not synced since the binding changed would otherwise show no symptom. Silent when the server is unreachable |
| `legacy_steering_pattern` | the operator's own steering text — the provider's instructions file outside the managed block, plus, for Kiro, the other `*.md` files beside `~/.kiro/steering/cartographer.md` — names a pre-D288 prefixed tool (`<prefix>__<tool>`, where `<tool>` is a real Cartographer tool and `<prefix>` neither is `mcp` nor contains `__`, so Claude Code's `mcp__<server>__<tool>` names are not flagged), which `sync` never rewrites there. Warning severity; the fix names the unprefixed tool ([D320](decisions/D320-upgrades-and-provisioning-tell-the-user-what-changed.md)) |
| `update_available` | a newer release is in the update cache (D254). Info severity; the fix is the channel's upgrade command. Cache only, so doctor stays offline |
| `unbound-residue` | no managed file comes from a KB no longer bound to the provider holding it (D170) — a projection predating an unbind, or a hand-edited lockfile. Only for providers with an explicit binding; a file with no recorded source (a lockfile written before D170) is unknown, not wrong, and never reported |

**Severities.** `error` — something is broken now (a managed file missing, a hook firing twice);
`warning` — something is stale or suboptimal (v1 lockfile, no trigger for a hook-less provider, a
version difference); `info` — context that cannot be acted on by itself and never changes the exit
code (managed entries recorded before content hashes existed, which nothing can verify). Findings
are printed errors first.

**`doctor --repair-hashes`** re-records the materialized hash of every managed entry that has none, computing it from the bytes already on disk, under the client-state lock (D157). It exists because the alternative for that narrow gap, `reconnect`, prunes and rewrites **every** managed artifact on every client. A backfilled entry is marked `adopted_at` in the lockfile: it is **adopted, not verified** — nothing was compared against the server, so drift is detectable only from the next server-side change onward. An entry whose file is *missing* is left alone: that is real drift for `sync` to fix, not something to paper over.

**Exit codes**: `0` clean, `1` findings (error or warning), `2` an error running the command — the
same convention `status` uses. An unreachable server is one `warning` finding, not a failure:
`doctor` stays useful offline.

**JSON shape**: `{schema_version, error_count, warning_count, info_count, findings[]}`, each finding
`{check, severity, message, path, fix}`. Flat and stable — it ends up in someone's monitoring.

The bootstrap hook and the scheduled timer deliberately do **not** run it: it is an operator command,
and running every check on every session start is exactly the background cost D60 avoided by keeping
the bootstrap script silent and deterministic.

### `cartographer kb repair <kb> [--apply] [--json | --revert <sha> [--reason <text>]]`

The unattended half of KB upkeep (D299), schedulable by the operator (cron, launchd, a CI job); nothing
installs a schedule. It calls `kb_status`, then `kb_repair` with `dry_run: true` for every check with a
mechanical fix, and prints per check the planned count and up to 5 examples. With `--apply` it re-runs
**only** the checks whose fix is safe to run unattended (D323, D355, D907: `lint.AutoRepairChecks`, `[auto]` in the
report) with `dry_run: false`; any other check is never applied by `--apply`. No setting narrows the list. The server applies the same checks by
itself every day, and each write already repairs the concepts it wrote
(D349, always since D907), so `--apply` usually finds little. It ends with the judgement
work left — the `kb_review` counts by kind — which only a `cartographer-kb-doctor` session with the operator handles.
It writes no `kb-doctor` log entry: a mechanical pass is not a doctor session, and the entry would
silence the server's doctor proposal while the judgement work is undone (each applied check still
leaves `kb_repair`'s own entry).

`--revert <sha>` replaces the repair: it calls `repair_revert`, which undoes one `kb_repair` or
auto-repair commit (its SHA is in the KB's `git log`, reason "auto-repair (background)") as a new commit through the server's
ordinary write path, and refuses any other commit.

The commits of `--apply` and `--revert` are authored as whoever runs the command (D904), by the
bridge's rule: the KB's `identities` entry, else `identities.default`, else what git resolves in the
folder the command runs in, and only inside a work tree. An empty entry sends no author, an invalid
one sends none with a warning; without an author the server authors the commit as the token. A
scheduled run outside any repository is therefore authored as its token unless `identities` names
someone.

Exit codes: `0` nothing left to do, `3` only judgement work or checks that are not safe to run unattended remain,
`2` error. `--json` prints the report as an object (`checks`, `review_total`, `review_by_kind`,
`remaining`). Not to be confused with `cartographer doctor`, which checks the client's provisioning.

### `cartographer kb reauthor <kb> [--apply] [--json]`

Recovery for a push a remote refused because of who authored or committed its commits (a membership
push rule, a private-email rule; D1063). It re-authors, under the identity the server file configures
for the KB (`git.identity` / `git.identities`, the rule `serve` applies at mount), the commits that exist
only in the local clone, then pushes. Without an identity configured it refuses: rewriting with the
fallback identity would reproduce the problem. The sync alone already re-authors a commit made by
exactly the server identity (D950); this command is for what it leaves alone, a commit authored by a
token (`<token id> <cartographer@host>`).

Like `kb unmount` it is offline and local (D177: `--data`, `--config`, `--local`), because it needs the
server config and the clone on this machine and operator authority, not a token. It takes the KB's
advisory process lock, so a running service does not have to be stopped, fetches `origin` with the KB's
credentials, and moves the branch with a compare-and-swap `update-ref`, so a write racing it makes the
rewrite fail instead of being lost. The working tree and index are never touched.

Without `--apply` it is a preview: every commit ahead of the upstream with its author, committer and
subject, marked `*` when it would change; nothing is written. `--json` prints
`{kb, branch, upstream, identity, commits:[{sha, author, committer, subject, rewrite}], apply, rewritten, pushed, error?}` (after a failed push, `rewritten` says the rewrite worked, `pushed` is `false` and `error` says why). With `--apply`, commits that already carry the identity are pushed without being rewritten.
`--apply` replaces author and committer, keeping message (so the `Cartographer-Author`,
`Cartographer-Token` and `Cartographer-Host` trailers stay as the record of who really wrote), tree and
dates, logs the old → new count on stderr (the branch reflog records the command), and pushes through the
normal path. A running service recomputes its sync status at its next sync or write.

It refuses, with exit `2` and a one-line reason, when: there is no identity configured; the directory is
not a git clone with an `origin`; `HEAD` is detached; a rebase or merge is in progress; the fetch fails (what
is only local must be proven against the remote); the branch has no upstream; it has diverged (behind the
upstream); the commits ahead hold a merge or a root commit; or any of them is already reachable from
another remote ref (published history is never rewritten). Exit `0` is done or nothing to re-author.

### `cartographer service sync-timer <action>`

The scheduled sync trigger (D140) for clients with no session-start hook, or one that fires only in
some sessions (Kiro, D300). With `auth: true` it authenticates from the token file `connect` wrote
(D699), since a timer inherits no shell exports. It is distinct from the
**server** service below, with its own unit files:

```bash
cartographer service sync-timer install [--interval 30m]
cartographer service sync-timer uninstall
cartographer service sync-timer status   # exit: 0 active, 3 installed but inactive, 4 not installed
```

| Platform | Files | Logs |
|---|---|---|
| macOS | `~/Library/LaunchAgents/com.cartographer.sync.plist` | `~/Library/Logs/cartographer/sync.log` |
| Linux | `~/.config/systemd/user/cartographer-sync.{service,timer}` | journal (`journalctl --user -u cartographer-sync`) |
| Windows | `%LOCALAPPDATA%\cartographer\tasks\sync.xml` → Scheduled Task `\Cartographer\Sync` | `%LOCALAPPDATA%\cartographer\Logs\sync.log` (via `sync --log-file`) |

`install` is idempotent (it overwrites and re-registers); uninstalling a timer that is not
installed is a success. The per-user scheduler belongs to the account, not to `$HOME`: when
`$HOME` is not the account's home directory (a sandboxed run, the e2e suite), the server service
and the sync timer write their unit files under `$HOME` and register nothing,
so a sandbox never replaces the account's own jobs (#683). The timer runs plain `cartographer sync`: there is no trust flag to give it (D906),
and an MCP descriptor still needs the local approval an unattended job cannot give. `setup` and `connect` install the timer themselves when a connected provider has no
session hook, or one limited to some sessions (D325); `setup` lists it in its plan. `uninstall` is
the explicit opt-out, remembered as `sync_timer_opt_out` in the client state file (so `connect` does
not reinstall it, and `doctor`'s `trigger` check is info rather than a warning); `install` clears
it. `disconnect` removes the timer when no remaining client needs it, without an opt-out. `status`
only names the command.

On Windows the trigger is a repetition at the configured interval with *start-when-available*
(systemd's `Persistent=true` analogue: a run missed while the machine was off happens as soon as it
is on), and the interval reported by `status` is read back out of the definition on disk, so there
is no second source of truth. The log file is what makes a failing background sync diagnosable
there: a task's own history records exit codes only, and is disabled by default on many machines
([D217](decisions/D217-the-native-service-on-windows-is-a-per-user-scheduled-task.md)).

### `cartographer service <action>`

Manages the **server** as a native user service on the machine (local mode, D73):
launchd on macOS, systemd user unit on Linux, a per-user Scheduled Task on Windows
([D217](decisions/D217-the-native-service-on-windows-is-a-per-user-scheduled-task.md)). None of the
three needs administrator rights. Client and server are the same binary: the client subcommands
talk to the daemonized server over loopback.

```bash
cartographer service install [--config <path>] [--data <dir>] [--http <addr>]
cartographer service uninstall|start|stop|restart
cartographer service restart --wait [--config <path>]   # graceful, version-gated (D121)
cartographer service status        # exit: 0 running, 3 installed but stopped, 4 not installed
```

Plain `restart` restarts the unit and returns. `restart --wait` gracefully replaces the process
(`SIGTERM`, so in-flight requests drain) and only prints success once `/health` proves the
installed binary version is serving from a new process (its `started_at` differs from the one
read before the restart, D266); `--config` selects the config used for that verification,
and is otherwise unnecessary because the installed service definition is discoverable.

Windows takes the same three verbs to a different scheduler, so two behaviours are worth naming.
`stop` also **disables** the task, because its logon trigger and its restart-on-failure would
otherwise bring the server straight back; `start` and `restart` re-enable it first, which is why a
service stopped on purpose stays stopped and a restart after a stop still works. And the graceful
replacement is not a signal but a named event in the user's session, followed by an explicit
relaunch of the task — nothing else would restart a process that drained and exited cleanly.
`restart` and `uninstall` wait for the old server to exit before starting or unregistering the task,
and from another logon session (SSH), where the event cannot be reached, the task is stopped
instead and the command says so (D266).

Operational details (generated paths, defaults, behavior with an existing config, automatic
repair on `install.sh update` and Cask upgrade) in `deployment.md` §Example: native local service
and §Upgrades, schema migration, and repo growth.

### `cartographer import`

A mechanical import scaffold (D74 WP2), a sibling of the agentic `cartographer-kb-import` skill
(`internal/skillbundle/bundled/cartographer-kb-import/`): unlike the other subcommands, it doesn't talk to the
server, it operates directly on a local clone of the KB (`--kb`). It walks the `.md` files under `--source`
(recursively, skipping hidden directories), maps each source directory onto a destination map (or
expanded concept), fills in the frontmatter (never overwriting a field already present), and writes via
`kb.Open`+`WriteConcept`. By default it leaves the working tree for the operator to review; `--commit`
creates one final commit containing only the paths written by that import.

```bash
cartographer import --source ./obsidian-vault --kb ./kb-clone \
  --default-map notes --map people=clients/people --dry-run
cartographer import --source ./obsidian-vault --kb ./kb-clone \
  --default-map notes --map people=clients/people
cartographer import --source ./docs --kb ./kb-clone \
  --default-map notes --dir-as-concept --commit
```

| Flag | Default | Description |
|------|---------|-------------|
| `--source` | *(required)* | Source directory to import |
| `--kb` | *(required)* | Local clone of the destination KB (already initialized) |
| `--default-map` | `""` | Default map for source directories with no `--map` (D77) |
| `--map` | *(repeatable)* | Per-directory mapping `<srcdir>=<map>` (`srcdir` relative to `--source`, `.` for the root) |
| `--dry-run` | `false` | Prints the mapping plan (source → concept id) without writing |
| `--commit` | `false` | Makes one final commit containing only import-written paths; pre-existing dirty work is untouched. Its author and committer are the operator, the identity git resolves for the KB's folder, else the server (D904, D950) |
| `--message` | `import: <source> -> <kb>` | Commit message; implies `--commit` |
| `--dir-as-concept` | `false` | Promotes a source directory with `index.md` (or `README.md`) into an expanded concept and keeps its satellites together |

**`--map` covers a subtree.** Resolution is longest matching prefix, then `--default-map`, then the
unmapped error (D162): `--map a/b=m` covers `a/b`, `a/b/c` and below, a more specific `--map a/b/c=n`
wins for its own subtree, and matching is at **segment boundaries**, so `a/b` never covers `a/bc`. `.`
is a legal source and covers everything, so a corpus with many source directories needs one flag per
subtree, not one per directory.

**The matched prefix is replaced, not appended to**: `--map a/b=m` sends `a/b/c/page.md` to `m`, not to
`m/c`. The destination is a *map* (or `map/expanded-concept`) and the write path caps concept depth at
three segments, so mirroring an arbitrarily deep source tree cannot work; preserving hierarchy is what
`--dir-as-concept` is for. Two `--map` flags with the same source are an error rather than the later
one silently winning, and a `--map` that matches nothing warns — otherwise a typo falls through to
`--default-map` unnoticed. `--dry-run` names the flag behind every destination, which is how you check
all of this.

**`import` takes the KB's advisory lock** and fails fast when the server holds it (D155), naming the
holder and the `service stop … && service start` sequence: it writes into the same directory the
server's sync loop manages, and the two interleaving corrupted the git index. `--dry-run` writes
nothing and never contends for it.

**The search index is not updated by this process.** `import` writes through the KB write path but
from outside the server, so the server's FTS index knows nothing about it: until it is rebuilt `search` returns nothing
while `concept_list` sees everything. On completion the command rebuilds the index when a configured server is reachable, and
otherwise prints the `cartographer reindex` instruction. It never changes the exit code: the import
itself succeeded.

**`import` rewrites markdown links.** Every `[text](path.md)` whose target is part of the same
import is rewritten to the destination's relative form, computed from the file it lands in — the
base lint uses since D149, so the importer's output no longer generates findings against itself.
Worth knowing before building a preprocessing step in front of it: if the caller has already
rewritten links into ID space, this pass either undoes that or leaves them unmapped. The workable
arrangement is to stage the corpus with its final map names and run `import` with identity
mappings, so the rewriting is a no-op. Wiki-links `[[id]]` are never touched.

For every file: if it already has YAML frontmatter it's preserved, only adding missing fields;
otherwise it synthesizes the minimum — `title` from the body's first H1 (fallback: file name), `type: Note` if absent
(`WriteConcept` always requires it — a deviation from the original spec, see
[D74](decisions/D74-import-of-external-non-okf-wikis-kbs-kb-import-skill.md)) — and
in both cases it ensures `status: imported`, hooking into the `imported_draft` lint (warning) that
keeps the curation backlog visible across sessions. Relative markdown links `[text](path.md)`
are rewritten best-effort against the new layout; wiki-links `[[...]]` are left as-is
(D72). A source directory with neither `--map` nor `--default-map` fails the command
(no write) with the list of unmapped directories. Final output: counts of files
imported/skipped (non-markdown)/errors — a write error on a single file does not block the
rest of the batch.

With `--dir-as-concept`, a directory containing `index.md` — or `README.md` when no
`index.md` exists — becomes `<map>/<directory>/`: the chosen file is written as that
expanded concept's `index.md`, while its sibling markdown files become satellites below it.
The dry-run labels the promotion explicitly. Without the flag, importing remains flat and a
source `index.md` is still rejected as a reserved destination filename. `--commit` also commits
the scaffold (`_map.md`, `index.md`, `log.md`) created for each new destination map; on partial
write failures it commits only successful paths and reports that the batch had errors.

### `cartographer resolve repo:<key>|path:<name>`

Resolves a path portability placeholder (D75) and prints the local path to stdout. It doesn't talk to the
server: it only reads `.cartographer.yaml` (`search_roots`, `paths`) and, if needed, scans the
filesystem (`internal/repoindex`) — it works even before a `connect`. It's the runtime fallback
for an agent that encounters, in a concept's body, a placeholder missing from the "Local
paths" table materialized in the instructions block — the block's placeholder paragraph points
agents here, and to `cartographer paths set` when resolution fails (`docs/sync.md` §Path portability placeholders), as well
as a standalone debugging tool.

When a key is not found, stderr carries, after the `Error:` line, one line naming the two commands that fix it (`cartographer paths set <kind>:<key> <path>` and `cartographer paths suggest`); stdout stays empty and the exit code is 1.

```bash
cartographer resolve repo:cartographer          # short form: key = last segment of the remote
cartographer resolve repo:github.com/org/nome   # full form: host/owner/name
cartographer resolve path:design-assets         # manual paths: mapping
```

A leading `~` is expanded in both `search_roots` and `paths` entries, followed by either
separator: `~/repos` and `~\repos` mean the same directory wherever the config is read. `~name` is
not expanded — another user's home is not something a config entry means.

A configured search root that does not exist, is not a directory, or cannot be listed produces a
**warning on stderr naming it and the OS error**, and the remaining roots are still scanned. It is a
warning, not a failure: a machine-local config may legitimately name a root that only exists on
another machine. The warning is separate from the depth message below, so a typo in a root is not
mistaken for a depth problem ([D216](decisions/D216-the-client-half-reaches-parity-on-windows.md)). The same warnings
surface in `AppliedResult.Warnings` when a `{{repo:…}}` placeholder is resolved during `sync`.

Exit code: `0` resolved (path on stdout), `1` not resolved (no `paths:` entry, no clone
found under `search_roots`, or an ambiguous key across several distinct remotes — error message on
stderr with the full form to use), `2` usage error (missing argument or not in the
`repo:...`/`path:...` form).

### `cartographer config [--json] [--all]`

Shows every setting with its effective value and where it comes from (D743), in two groups: the client settings of
`.cartographer.yaml` (origin `file` when the key is written there, otherwise `default`) and, when the configured server
answers `GET /api/ui/v1/settings` with the client's token, the server's: its keys, then one group per KB. One line per
key: `key  value  (origin)`, with `restart required` when the schema says so. By default only the keys something set are
listed; `--all` adds the rest with their default. A per-KB key the KB's own `settings.yaml` sets shows origin `kb`, which beats `file`, `env` and `flag` (D746). A secret shows `(set)` or `(unset)`. `--json` prints the raw
structure (`client`, `server`, `note`). A server that is unreachable, refuses the token or predates the route costs a
one-line note instead of the server group, and the exit code stays 0. The same data is the Atlas Settings page
(`control-plane.md` §Read-only UI API).

The Atlas Settings page also shows the client, as `sync` last reported it: after every apply, `sync` posts a report to
`POST /api/client-settings` (`control-plane.md` §Client settings reports): the client schema keys with their origin, the
`.cartographer.yaml` as on disk, the connected agent clients (scope, entry kind, bound folders whatever the scope, and their
KBs) and where each placeholder of the connected KBs resolves on this machine. No secret value, token or file content other
than that config file. The server keeps it in memory for the sending principal only. Best-effort: a server that is
unreachable, predates the route or refuses the token costs a debug line, never the sync (D818).

### `cartographer paths [list [--json]] | set <key> <path> | ignore <key> | unset <key>`

The placeholder keys the last sync met, and the command that records where they live on this
machine (D262). No network call: everything is read from the lockfile and `.cartographer.yaml`.

```bash
cartographer paths                          # = paths list: every key, its KB(s), path or failure reason
cartographer paths list --json              # {"placeholders": [{key, kbs, path | reason, description, default, ignored}]}
cartographer paths set path:kubeconfig ~/.kube/config
cartographer paths set repo:dotfiles ~/src/dotfiles
cartographer paths ignore repo:work-tools      # never exists on this machine: stop reporting it (D282)
cartographer paths unset kubeconfig           # remove a mapping, or an ignore
cartographer paths suggest                  # a candidate path, as a `paths set` line, for every unresolved key (D320)
cartographer paths --help                   # the usage above, on stdout, exit 0 (also `paths help`)
```

- `suggest` reads the same unresolved, not ignored keys and prints, per key, its KBs, its
  description and one `cartographer paths set <key> <path>` line per candidate — or a line saying
  there is none. Candidates, in order: the `paths.yaml` default, when it exists on this machine;
  for a `repo:` key, every clone under `search_roots` whose origin remote contains the key name or
  whose directory is named after it (the near misses sync's exact match refused); for a `path:`
  key, `~/<name>`, `~/.config/<name>` and `~/.<name>`. It writes nothing.
- `list` shows every key recorded in the lockfile, resolved or not, with the KBs citing it (from
  `sync_pull`'s `placeholders` list and the artifacts) and the local path or the reason it failed.
  When a bound KB declares the key in its `paths.yaml` (D263), an indented line below shows its
  `description` and declared `default` (both also in the JSON); the first, tab-separated line keeps
  its shape.
- `set` writes `paths:` in `.cartographer.yaml` (`clientconfig.Save`: other keys survive). The key is
  accepted with or without its `repo:`/`path:` prefix and stored **without** it, which is how
  resolution looks it up — one map serves both kinds, `paths:` being checked before the repo cache
  and scan. The path is stored as written (`~` kept, expanded at resolution time). A path that does
  not exist is recorded anyway with a warning (you may be about to clone it); for a `repo:` key, a
  path that is not a git clone is a warning too, never an error.
- `ignore` marks a key as deliberately absent on this machine (D282), for a work repo on a personal
  laptop or a folder that exists on another host only. It is stored in `ignored_paths:` in
  `.cartographer.yaml` with its kind prefix (`repo:work-tools`); an unprefixed argument is qualified
  from the lockfile and refused (exit `2`) when the lockfile does not know it. Resolution is
  untouched — the placeholder stays verbatim, and a key that does resolve still does — only the
  reporting changes: the sync warning, `status` and the connect step stop naming it, and `list` shows
  it as `IGNORED` (`"ignored": true` in the JSON) instead of counting it unresolved. A key mapped under
  `paths:` cannot be ignored (exit `1`), and `set` on an ignored key lifts the ignore.
- `unset` removes an entry or an ignore (exit `1` if there is neither).
- `set`/`ignore`/`unset` print the one command that applies them — `cartographer sync` — and never sync
  implicitly. They need an existing `.cartographer.yaml` (exit `2`, run `connect` first).

The TUI dashboard's per-provider sync reports the unresolved count in its done message; editing
happens here, not in the TUI.

## Adding a provider

Every supported provider is one descriptor in `internal/configurator/registry.go`
([D137](decisions/D137-declarative-provider-registry-two-tables-owned-by-the.md)): its `Provider` constant and wire value, display
name, native MCP config file and format (`FormatJSON` with its server key, or `FormatTOMLBlock`),
whether that file may be deleted once emptied (never for Claude Code — `.claude.json` is Claude's
own shared state), whether it can carry MCP auth headers, whether its MCP tool namespace is flat
across servers, the longest tool identifier it accepts (`ToolIdentifierLimit`, which `connect`
and `sync` check), the client majors whose hook plugin shape is known (`HookPluginMajors`, which
`doctor` checks), the global instructions files that shadow the managed one
(`InstructionsPrecedence`, which `doctor` checks), the detection evidence (binary names, config directories in probe order, optional
env-anchored config directories, optional per-GOOS application directories), and its emitter
function.

Two orders are exposed and both are user-visible: `Providers()` — the order `EmitAll` and the
client subcommands iterate — and `DetectionOrder()`, the order `cartographer agents` and the TUI
list agents in.

Adding a provider therefore means: one descriptor, one emitter (provider output formats genuinely
differ, so that stays code), its cells in the kind × provider matrix (`internal/provisioning`, see
[`sync.md`](sync.md) §Kind × provider matrix), and — if it has a native hook mechanism — one entry
in `hookMechanisms`. A missing matrix cell fails a completeness test; nothing else needs editing.
Every provider declares a config file and an emitter, and materializes under the shared base dir
([D898](decisions/D898-hermes-support-is-removed.md)).

A configuration that still names a client the registry no longer knows keeps working: `Load` drops the
name (and its bindings) in memory, `sync`/`connect`/`status`/`doctor` print once
`<name>: this agent client is no longer supported …`, the first writing command removes it from
`.cartographer.yaml` and drops its lockfile section, and the files delivered earlier are never touched.

## Files generated per provider (HTTP transport)

| Provider | Generated file | Key |
|----------|--------------|--------|
| Claude Code | `.claude.json` (also `<dir>/.claude.json` for each `clients.claude.config_dirs`, D755) | `mcpServers` (JSON) |
| Codex CLI | `.codex/config.toml` | managed block `[mcp_servers.cartographer]` (TOML, marker `cartographer:mcp:*`) |
| Kiro | `.kiro/settings/mcp.json` | `mcpServers` (JSON) |
| OpenCode | `opencode.json` | `mcp` (JSON) |
| Google Antigravity | `.gemini/config/mcp_config.json` | `mcpServers` (JSON) |
| Crush | `.config/crush/crush.json` | `mcp` (JSON) |
| GitHub Copilot CLI | `.copilot/mcp-config.json` | `mcpServers` (JSON) |

KB-provided stdio descriptors (D116) share these same files with per-name ownership. Claude Code,
Codex and Kiro receive native `command`, `args` and `env` fields (Kiro also keeps `autoApprove: []`);
OpenCode uses `type: "local"`, an ordered command array and `environment` with `{env:VAR}` references;
Crush uses `type: "stdio"` with a string `command` plus a separate `args` array.
Copilot CLI uses `type: "stdio"` with `command`, `args` and `env`, and every entry carries `"tools": ["*"]`.
Cartographer only preflights the local executable before writing: it never runs it, and never resolves
an environment reference into its value.

## Format of the generated files

**Claude Code** — with auth:
```json
{
  "mcpServers": {
    "cartographer": {
      "url": "http://127.0.0.1:39273/mcp",
      "type": "http",
      "headers": { "Authorization": "Bearer ${CARTOGRAPHER_TOKENS}" }
    }
  }
}
```

**Codex CLI** — with auth (managed block in `.codex/config.toml`, never parsed/re-serialized:
only the text between the markers is touched, via `internal/blocktext`):
```toml
# cartographer:mcp:begin
[mcp_servers.cartographer]
url = "http://127.0.0.1:39273/mcp"
bearer_token_env_var = "CARTOGRAPHER_TOKENS"
# cartographer:mcp:end
```

Codex CLI rewrites `config.toml` whenever it saves its own settings, re-emitting the
tables in canonical form and dropping every comment — the markers with them. `connect`
and `sync` reconcile this: before writing a block they remove the copies of the tables
that block owns (the MCP entry, and the hook registrations of D58) left elsewhere in the
file, which would otherwise be duplicate keys and stop Codex from starting, and report
each removal as a `warning:` line. Everything else in the file — comments, ordering,
unrelated tables, Codex's own `[hooks.state."…"]` bookkeeping — is left as it is (D99).

Recognizing a hook's own orphaned registration among those copies cannot rely only on a
path fragment into the hook's materialized directory (D99's original identity): a hook
whose command is a self-contained inline one-liner (e.g. a `jq ...` command, not a script
file) never contains one. `connect`/`sync` also match on the registration's `command`
*value*, decoded regardless of which of the four TOML string forms it is spelled in —
Codex re-serializes a command Cartographer wrote as a basic string (`"…"`) into a
multi-line literal string (`'''…'''`) — and compared byte-exact against the command the
hook currently registers. Both identities are accepted, so an older client's
path-fragment-only registrations are still adopted (D127). A hook whose command changed
in the narrow window between a Codex rewrite and the next sync matches neither identity
and is left duplicated — accepted as a residual, two-fault edge case; see D127.

Codex also places that same `[hooks.state."…"]` bookkeeping positionally after the last
table it finds in the file — which, once a block has been written, is the one Cartographer
owns. Before rewriting a block, `connect`/`sync` first relocate any table the block does
not itself declare out of the span, verbatim, to just before the block's begin marker, so
the next `blocktext.Write` cannot destroy it; each relocation is reported as its own
`warning:` line (D126). Purely textual, like every other step of this reconciliation:
`config.toml` is never parsed/re-serialized (D58).

**Kiro**:
```json
{
  "mcpServers": {
    "cartographer": {
      "url": "http://127.0.0.1:39273/mcp",
      "type": "http",
      "autoApprove": []
    }
  }
}
```

**OpenCode** (schema: https://opencode.ai/config.json):
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "cartographer": {
      "type": "remote",
      "url": "http://127.0.0.1:39273/mcp",
      "enabled": true
    }
  }
}
```

**OpenCode** — with auth (OpenCode's native `{env:VAR}` syntax):
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "cartographer": {
      "type": "remote",
      "url": "http://127.0.0.1:39273/mcp",
      "enabled": true,
      "headers": { "Authorization": "Bearer {env:CARTOGRAPHER_TOKENS}" }
    }
  }
}
```

> **Known risk**: OpenCode is SSE-first and support for custom headers on a remote MCP
> may require `mcp-remote`/`mcp-auth.json`.

**Google Antigravity** — with auth (Antigravity natively resolves `${VAR}` in headers):
```json
{
  "mcpServers": {
    "cartographer": {
      "url": "http://127.0.0.1:39273/mcp",
      "headers": {
        "Authorization": "Bearer ${CARTOGRAPHER_TOKENS}"
      }
    }
  }
}
```

**Crush** — with auth (schema: https://charm.land/crush.json; Crush expands `$VAR` in config
values, so the `${VAR}` of the other providers is rewritten into its documented unbraced form):
```json
{
  "$schema": "https://charm.land/crush.json",
  "mcp": {
    "cartographer": {
      "type": "http",
      "url": "http://127.0.0.1:39273/mcp",
      "headers": { "Authorization": "Bearer $CARTOGRAPHER_TOKENS" }
    }
  }
}
```

Crush's current configuration format is Bash (`~/.config/crush/crushrc`, executed at startup) and
the JSON above is the one it documents as deprecated — written deliberately: merging into a client's
config must be non-destructive, which a JSON object allows and a script does not, and an entry in
`crushrc` would *run* in the user's session rather than be read
([D225](decisions/D225-crush-is-a-provider-and-its-config-is-the-json.md)). Because Crush evaluates
config values, a header or `env` value carrying a `$(command)` substitution is **refused** with an
error naming the server and the key, instead of being written for Crush to execute on its next start.
Crush has no documented user-level subagent directory, so that artifact kind is unsupported for it.
Its hooks (`hooks.PreToolUse` in `crush.json`, D363) are registered by name; only `PreToolUse` fires,
so there is no session event and its bootstrap trigger is the scheduled timer.

**GitHub Copilot CLI** (`copilot`, D676; probed on 1.0.94) — with auth. The file is
`~/.copilot/mcp-config.json`; Copilot expands `${VAR}` in a header value from its environment, so the
header is written as for Claude Code. `"tools": ["*"]` is required: without it a non-interactive
session is offered no tool of the server:
```json
{
  "mcpServers": {
    "cartographer": {
      "type": "http",
      "url": "http://127.0.0.1:39273/mcp",
      "headers": { "Authorization": "Bearer ${CARTOGRAPHER_TOKENS}" },
      "tools": ["*"]
    }
  }
}
```

Copilot's MCP tools are named `<server>-<tool>` (for example `cartographer-concept_new`). Its other
global cells are `~/.copilot/copilot-instructions.md` (a managed block), `~/.copilot/skills/<name>/`,
`~/.copilot/agents/<name>.agent.md` (frontmatter `name` and `description`) and hooks. A hook is a
dedicated file `~/.copilot/hooks/cartographer-<name>.json`
(`{"version":1,"hooks":{"<event>":[{"type":"command","bash":…,"powershell":…}]}}`) that Cartographer
owns whole (the hook's own files sit in `~/.copilot/cartographer-hooks/<name>/`, since Copilot parses every `*.json` below `~/.copilot/hooks/`): it never edits `~/.copilot/settings.json`. Only `SessionStart`, `PreToolUse` and
`PostToolUse` are registered (`sessionStart`, `preToolUse`, `postToolUse`); Copilot has no matcher, so a
KB hook that declares one is installed but not registered, with a warning. Cartographer reads
`$HOME/.copilot` like every other provider and ignores `COPILOT_HOME`. The project-local MCP, agent and
hook locations are gated on folder trust and are unsupported ([D676](decisions/D676-github-copilot-cli-is-a-provider.md)).

The formats above are generated from the same provider-neutral core,
`configurator.EmitServer(name, spec ServerSpec, provider)` (D69): `Emit(cfg, provider)` is a
thin wrapper around `EmitServer(cfg.Name, cfg.toSpec(), provider)`. The same `EmitServer` is
reused by `internal/provisioning` to materialize the third-party MCP servers a KB
distributes (`mcp/<name>.json`, kind `mcp`) — not Cartographer's own entry, but any
server, with per-name ownership in the same file (`mcpServers.<name>`/`mcp.<name>`/block
`[mcp_servers.<name>]` marked `# cartographer:mcp:<name>:begin/end`). Details →
`docs/sync.md` §MCP servers.

## `.cartographer.yaml`

### Approval of MCP descriptors, hooks and executable files

Every MCP descriptor a KB ships, **signed or not**, needs a separate, local approval: an MCP server
receives the agent's data, a signature proves only which server a descriptor came from, and since D906 any
`rw` token on the KB can write a descriptor the server then signs. A KB hook, any KB artifact that ships
a file with the execute bit, and a skill or agent whose frontmatter declares `hooks` or MCP servers (also in a
`providers:` block) need the same approval because they run on this machine (D1141), signed or not. Nothing else the client installs
waits for a decision (there is no `trust` setting and no `--auto-trust`), and bundled artifacts never do.
Inspect and record the current content with `cartographer approve <kind> <name> --kb <kb> --yes`
(`kind` is `mcp`, `hook`, `skill` or `agent`; interactive terminals default to no confirmation; for a
non-MCP kind the command lists the files and prints the `hook.json` and the content of the executable files). Approval records the source KB,
artifact name (`<kind>/<name>` for everything but an MCP descriptor), content hash and timestamp in
`.cartographer.yaml`; a content change requires reapproval. `cartographer approve revoke <kind> <name> --kb <kb>`
is idempotent and takes effect on the next `cartographer sync`: for an MCP descriptor it prunes the managed
provider config; for a hook or an executable file it stops refreshing what is installed, which is left in
place, registered and running (the command says how to remove it). A hook installed before approval existed is reported as pending on its first sync and is not
refreshed, registered anew or removed until approved.

Written/updated by `connect` in the user's home directory (`~/.cartographer.yaml`, machine-wide —
`clientconfig.TargetDir`, D52): it records which server the machine is connected to (the connected providers are in the client state file, below). One file per machine, not per project: this avoids drift with a provider
connected in one repo but not another.

```yaml
server_url: http://127.0.0.1:39273/mcp
server_name: cartographer  # name under which the server is registered in the MCP configs (always "cartographer", overridden only by editing this file)
auth: false
token: ${CARTOGRAPHER_TOKENS}   # a literal token or ${ENV}; a literal keeps this file at mode 0600 (D906). Unattended runs fall back to the private token file
clients:         # per-provider KB binding (D169); absent provider = every known KB
  claude:
    kbs: [homelab]
    config_dirs: [~/.claude/accounts/b]   # extra Claude Code config dirs (CLAUDE_CONFIG_DIR) whose .claude.json also gets the MCP entry (D755); claude only; does not make the binding explicit
search_roots: ["~/Documents", "~/src", "~/code", "~/projects"]   # where repoindex.Scan looks for git clones for {{repo:<key>}} (D75); the default, 4 levels deep (a constant, D906); a default root that does not exist is skipped silently; consulted only for a key with no `paths` entry
paths: {}                       # manual name -> path mapping for {{path:<name>}} (and an override for {{repo:<key>}}, D75); edit with `cartographer paths set`
ignored_paths: []               # placeholder ids known to be absent on this machine, e.g. repo:work-tools (D282); edit with `cartographer paths ignore`
identities:                     # D904; the git author of this machine's writes, by KB name or default; without an entry, the identity git resolves in the session's repository (an HTTP entry: the global one)
  default: Ada Lovelace <ada@example.com>
  work-kb: Ada Lovelace <ada@corp.example>   # an entry decides: "" sends no author (the server names the token), an invalid one sends none and `sync` warns
allow_insecure_http: false      # D1023; written only when true. true: accept a plain http:// server_url that is not loopback (the token then travels in clear); set by `connect --allow-insecure-http`
update_check: true              # D254, D906; written only when false. false: no update lookup at all. The client never installs a release by itself
usage_scan: true                # D326; written only when false. false: sync reads no session transcript and reports no skill usage (docs/sync.md §Usage scan)
scopes: {}                      # per-provider scope, provider | workspace (D193); set by `cartographer workspace bind`
workspaces: {}                  # per-provider explicit workspace bindings, [{path, kbs}] (D193); set by `cartographer workspace bind`
signing_keys: {}                # Ed25519 public keys pinned per source KB (`connect --pin-key`); never learned from a sync
mcp_approvals: {}               # local approvals of MCP descriptors, bound to the content hash; written by `cartographer approve`, not edited by hand
```

Every key, with its values and default, is the `Schema` table in `internal/clientconfig/schema.go`.

The write-findings hook (D353) is always installed (Claude Code, Codex, ...); `docs/sync.md` §Layer 1.

**The file documents itself (D747).** Every key it holds carries a comment (what it does, then one line
with its values, default and notes), and the keys that are not set are listed commented out under
`# --- available settings: uncomment a key to set it ---`, like a Helm `values.yaml`: a blank line between
keys, each with the value to uncomment, and an example under a map or a list (`# scopes:` /
`#   claude: workspace`). Files written before that wording have `# --- available settings ---`; a realignment
reads either divider and writes the new one. The text comes from one table, `Schema` in
`internal/clientconfig/schema.go`, which is also what `config.example.yaml` and the config view read;
a test fails when a key exists in the file without an entry there, so a new setting starts in the
table. `Save` is a `yaml.Node` round-trip of the existing file: a comment you wrote and a key the
program does not know survive every save, a key you had commented out leaves the block when it gets
set, and a file written by an older version gains the comments at its next save. A save that changes
no setting does not rewrite the file. A file with no comments loads exactly as before: comments are
additive.

**Realignment on `sync` (D903).** The first `cartographer sync` after an upgrade that removed a key
brings the file up to date, under the client-state lock: a removed key is dropped, a value that switched
a feature off is reset to the default, the comments above are written, every other value, comment and key
order stays, and the original is kept beside it as `.cartographer.yaml.bak-<version>` (a numeric suffix
when one of that name exists). `sync` prints what it changed; a second run changes nothing and writes no
backup, and `sync --dry-run` writes nothing. The registry of removed keys is `Retired` in
`internal/clientconfig/realign.go`, next to the schema. A removed key met later, edited back in by hand,
is ignored with a warning that names its replacement; it is never an error.

The registry holds the client keys `token_env` (rewritten as `token: ${NAME}`, comments kept),
`trust`, `write_findings_hook`, `search_depth`, `update.check` and `update.policy` (`update_check`, which an
`auto-patch` policy turns into `true`), and `agents` and `sync_timer_opt_out`, whose **values move** to the
client state file before the YAML drops them. A realignment that cannot write the state file stops and
leaves the keys where they are. Until a file is realigned (a read-only mount), `Load` honours the old
`token_env`, `update.check`, `agents` and `sync_timer_opt_out`.

**Machine-written records live in `.cartographer-state.json`**, next to `.cartographer-client.lock` and
`.cartographer-sync.lock.json` (D747). `known_kbs` (the KB names `/health` advertised at the last
`connect`/`sync`), `server_mount_mode` and `server_routed_path` are rewritten wholesale by every
sync, which is why they left the file you edit; since D906 it also holds `agents` (the connected clients,
maintained by `connect` and `disconnect`) and `sync_timer_opt_out` (a record of `service sync-timer
uninstall`), for the same reason: they are written by commands, never by a person. Because of them it is
**not disposable**: a deleted state file means no connected client until the next `connect`. A sync that only refreshes them writes the state file
and leaves `.cartographer.yaml` byte-identical, mtime included. The state file is JSON, never
hand-edited. An installation from before keeps these keys in `.cartographer.yaml`; `Load` reads them
from there while the state file is absent, and the next save moves them (the legacy `kbs` key, read
since D169, goes the same way). An unreadable or corrupt state file is treated as absent, with one
line on stderr: the caches in it are rebuilt by the next `sync`, the connected clients by `connect`. `clients` is user-owned: `sync` never writes it, and `connect` writes a provider's binding only on its first connect, when it has none (the KB selection, `--kb`); it is otherwise maintained with `cartographer client` (below).

### `cartographer client`

Declares which Knowledge Bases each connected provider may receive. Every
subcommand works offline: it reads and writes `.cartographer.yaml` only and
never contacts the server, so a KB name that is not currently advertised is a
warning, not a failure.

| Command | Effect |
|---|---|
| `client list` | one row per connected provider: resolved KBs and the origin of the answer (`explicit` / `default (all known)`) |
| `client show <provider>` | that provider's bound KBs, its origin, and the known KBs it is **not** bound to; for `claude` also a `config dirs` line: `$HOME` (default), each declared dir, and an undeclared `CLAUDE_CONFIG_DIR` marked `not declared` |
| `client bind <provider> <kb>[,<kb>...]` | adds to the binding (the output lists the resulting binding and what this call added); creating the first binding narrows the provider from "every known KB" to only those listed, and the output says so; before warning that a KB is unknown it asks the server for its KB list once, without rewriting the cached list (`sync` owns it, D721) |
| `client unbind <provider> <kb>[,<kb>...]` | removes; removing the last KB leaves the provider bound to **no** KBs |
| `client reset <provider>` | deletes the binding, returning the provider to the default |
| `client update [--check=true\|false]` | shows, or sets, `update_check` (D254, D906) |

Three states, resolved only through `clientconfig.Config.BoundKBs` and never by
testing a list for emptiness: **no entry** means every known KB (today's
behaviour, so an upgrade never strips artifacts from an already connected
client); **an entry holding an empty list** means no KBs; **an entry holding
names** means those. `default-deny` is what declaring an entry buys, not a
global mode — an operator who wants it everywhere declares a binding per
provider.

`bind`, `unbind` and `reset` save the configuration and stop: they never
trigger a sync, and print `run cartographer sync to apply`. `bind` also warns,
best-effort, when the new binding creates a cross-KB collision (D171); an
unreachable server makes that check skipped, never a failed command.

The binding governs the whole projection, not just artifacts: a provider's MCP
entries are emitted for its bound KBs only. Two rules there are easy to get
wrong and are pinned by tests — the entry **shape** comes from what the server
mounts (a bare `/mcp` auto-routes only when the server mounts exactly one KB, so
a client bound to one of four still needs `?kb=`), while the entry **set** comes
from the binding; and entry *removal* is driven by the union of every known KB,
never by a provider's filtered list, or an unbound KB's entry would be orphaned
forever.

`cartographer status` shows each provider's bound KBs and the origin of that
answer, plus a per-KB breakdown of what it currently holds, read from the
lockfile's recorded source.

`status` and `sync` read this file (via `internal/clientconfig`): without `.cartographer.yaml` they
fail with exit 2, suggesting `connect` first (`cartographer resolve` is the exception:
it works even without it, using `clientconfig.Default()`'s defaults).

## Lockfile v2 multi-provider

`.cartographer-sync.lock.json`, written by `connect`/`sync`, records for **each provider** what has
been materialized:

```jsonc
{
  "providers": {
    "claude": { "applied_revision": "9f86d081…", "server_version": "1.4.0", "managed": [ /* ManagedFile[] */ ],
                "resolved_placeholders": { "repo:tool": "/home/user/src/tool" },
                "unresolved_placeholders": { "path:kubeconfig": "no \"kubeconfig\" entry under paths: (.cartographer.yaml)" },
                "placeholder_sources": { "repo:tool": ["kb-a"], "path:kubeconfig": ["kb-a"] },
                "placeholder_decls": { "path:kubeconfig": { "description": "kubectl config" } },
                "paths_section_hash": "…" },
    "opencode": { "applied_revision": "9f86d081…", "server_version": "1.4.0", "managed": [ /* ManagedFile[] */ ] }
  },
  "workspaces": { "/path/to/repo": { /* provider → Lock: a separate namespace */ } }
}
```

`applied_revision` is the bare hex sha256 of the manifest. A lock also carries `base_dir` and
`project_scope` for a workspace projection, and `session_hook_limit_acked` (field by field in
`docs/sync.md` §Core concepts).

The old v1 format (`{"applied_revision", "provider", "managed"}`, single provider) is
automatically migrated on read (`provisioning.ReadLockFile`) into `{"providers": {<provider>:
{...}}}`. See `docs/sync.md` §Core concepts for the full model (drift, pruning).

## TUI mode (interactive dashboard)

Running `cartographer` with no arguments in a terminal opens an interactive dashboard
(`cmd/cartographer/tui.go`, `bubbletea`): a server block, then one card per provider with an
explicit status (`connected` / `not connected` / `not installed`) and indented details below,
laid out on a two-column grid:

```
server     http://127.0.0.1:39273/mcp  in-sync · ready
version    client v0.10.0 · server v0.10.0
service    local: installed · loaded
KBs        kb-uno (2 bound) · kb-due (1) · kb-tre (0) · kb-quattro (0)

> Claude Code    connected
      binary      /opt/homebrew/bin/claude
      mcp-config  in-sync
      kbs         kb-uno, kb-due  (explicit)
      artifacts   in-sync
      kinds       skill 5/5 · agent 4/4 · hook 2/2 · instructions 1/1
```

`binary` and `kbs` are local data and are in the first frame; `artifacts` and `kinds` are
fetched asynchronously against the configured server.

- **`kbs`** is the provider's own binding (D169): the declared names with `(explicit)`,
  `all known (default)` when nothing was declared, and `none (explicit)` for a binding
  deliberately emptied. A list too long for the row is truncated with a counter
  (`kb-uno, kb-due +2`) and never wrapped.
- **`kinds`** is the per-kind breakdown `formatKindStatus` produces, the same one
  `cartographer status` prints — both read it off the shared snapshot. It says `unknown`
  before the first fetch resolves and after one that failed: a breakdown computed against a
  manifest that was never fetched is not a clean bill of health. `no artifacts` is the
  distinct case of a manifest that *was* read and holds nothing.

Main keys:

| Key | Action |
|---|---|
| `enter` | Connect (if not connected) or resync the **selected** provider |
| `s` | Resync the **selected** connected provider (no-op on a disconnected one) |
| `S` | Sync every connected provider, one at a time under a single client lock (D172) |
| `d` | Disconnect (if connected) — opens an inline `y`/`n` confirmation naming its KBs |
| `r` | Refresh status |
| `q` / `ctrl+c` | Quit |

`d` on a connected agent opens a confirmation screen (`y` confirms; the arrows, `tab`, `h` and `l`
toggle Yes/No and `enter` confirms only when Yes is selected; `n`, `Esc` and `q` cancel and return to the
list; any other key is ignored); on a non-connected agent it's a no-op. The TUI is a
subset of the CLI: it uses the same `doConnect`/`doDisconnect` logic as `cartographer
connect`/`disconnect`, but doesn't expose `--dry-run`. Outside a TTY, `cartographer`
with no arguments prints the usage (like `cartographer help`).

## Non-destructive merge

If a config file already exists, `connect` reads the existing content, adds or
updates only the server's key (`server_name`, default `cartographer`) and rewrites the file. Other configuration present
in the file (other MCP servers, other keys) is left untouched. `disconnect` performs the
inverse operation (`configurator.Remove`): removes only the server's key from the `mcpServers`/`mcp` map and
rewrites the file, leaving everything else intact (other MCP servers, other top-level keys).

### Symlinked config files and directories are refused

Every write, mkdir and remove under a client directory goes through `internal/safewrite`, the same
guard that protects the provisioned artifacts (`sync.md` §Security): the provider's MCP config file
and the Codex `config.toml` blocks `connect`, `sync` and `disconnect` edit (`internal/blocktext`),
the instructions block, the hook registrations (`settings.json`, `hooks.json`, the Kiro, Copilot,
Crush and OpenCode files), the files `disconnect` and a sync prune, and the workspace's
`.git/info/exclude`. `os.WriteFile` on a symlink opens the link's *target*, and `os.Remove` or
`os.MkdirAll` under a linked directory act in the target too, so a config file that is a link, or a
directory above it under the client base dir that is one (a dotfile manager, a shared checkout), would send the
operation somewhere else. Both are refused, with an error that names the path and says to replace the
link with a regular file or directory; so is any other non-regular file or non-directory (device,
socket, named pipe, Windows reparse point). The base dir itself (`$HOME`, or the project dir in
workspace scope) may be a link, and a path that does not exist yet is created.

A refusal needs something to write or remove: a config with no entry or block of ours in it is a
no-op under a link too, never an error, so a dotfile-managed `~/.codex/config.toml` that needs no
change stays quiet. `--dry-run` refuses too, and previews a prune's refusal for the managed file
itself and for a shared config file that exists; it can flag a linked MCP config the real run would
leave untouched, and it does not check the file that holds a hook's registration.

The refusal is **per provider** (and, in a sync, per artifact):
- `connect` and `sync` report it as a warning (`mcp config left unchanged: …`, or
  `<kind>/<name> not installed: …` / `not removed: …` for a provisioned artifact: a skill, an MCP
  entry, the instructions block, a hook and its registration) and still write every other provider and
  artifact. The bootstrap and write-findings hooks are no exception: a provider whose
  destination for them is behind a link gets a `<provider>: bootstrap hook not installed: …` warning and the
  others are still set up. A refused write of a new artifact is not recorded as installed, a refused update keeps the previous
  lockfile entry (its files on disk are still ours), and one whose removal was refused stays in the
  lockfile, so the next sync retries each. A hook's script is only removed once its
  registration is. When a stale per-KB entry could not be removed, that KB (only) stays in
  `known_kbs` until the entry is gone. A workspace's `.git/info/exclude` behind a link is a warning, not
  an error.
- `disconnect` prints `kept connected: …` for the provider, leaves its lockfile section,
  `.cartographer.yaml` entry and managed files (whatever could be pruned is) in place so the next
  `disconnect` retries, removes the others, and **exits 1**. The TUI shows the same line. `reconnect`
  does not rebuild a provider its disconnect half kept, rebuilds the others, and also exits 1.

Nothing is written through the link until the operator replaces it. The legacy `.codex/config.json`
that `disconnect` cleans up is checked before `config.toml`, so a refusal there leaves both untouched.

## Installation

One channel per platform, and an upgrade uses the one the binary came from:

```bash
brew install beppetemp/tap/cartographer     # macOS (cask from the tap)
irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex   # Windows (PowerShell, D252)
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
```

`install.sh` downloads the latest binary from the GitHub Release for the current platform (darwin/linux ×
amd64/arm64), verifies the checksum against the release's `sha256sums.txt` (a missing manifest is an error, D1022), and installs it into
`/usr/local/bin` (or `~/.local/bin` if not writable). Also supports `update` and `uninstall` as the
first argument. Run from a Windows shell (Git Bash, MSYS2, Cygwin) it refuses and prints the `install.ps1`
command instead. `install.ps1` is the Windows counterpart (install/`update`/`uninstall`, per-user,
checksum-verified). See `docs/deployment.md` §Client installation.
