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
root and `service` help write to stdout and exit 0. Unknown commands only offer
a correction for one command within edit distance two.

`agents`, `status`, and `service status` accept `--output table|json` (table is
the default). JSON is written only to stdout and uses schema
`cartographer.status/v1`: it includes the schema version, effective server URL,
client/server facts, provider states and artifact counts; when relevant it also
contains native-service facts. State and error `code` fields are stable for
automation; a wrapped low-level `cause` is retained in JSON only. `status`
keeps exit 0 for in-sync, 1 for drift and 2 for configuration or operational
errors. `service status` retains 0 running, 3 stopped and 4 not installed.

### `cartographer update check|notice`

Tells an agent, and its user, that a newer release exists
([D254](decisions/D254-agents-are-told-when-cartographer-is-out-of-date.md)). The source is the
GitHub release list (`/releases?per_page=1`, which includes the pre-releases every 0.x tag is),
cached for 24 h in `<user cache dir>/cartographer/update-check.json`. A lookup times out after 3 s
and any failure means "unknown": nothing is printed. A `dev` build never checks.
`CARTOGRAPHER_NO_UPDATE_CHECK=1` or `update.check: false` (below) turns it off;
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

With `update.policy: auto-patch`, a **patch** release through `homebrew`, `install.sh` or
`install.ps1` installs itself: `update notice` or the next `sync` (the scheduled one included)
starts a detached `update apply`, guarded by a lock in the cache directory so concurrent sessions
start it once, logging to `<cache>/cartographer/update.log`. The next session's notice reads
`Cartographer patched itself to vX; restart your agent sessions to load it.` once; a failed apply
leaves the binary as it was and the notice reverts to the manual form, naming the log. Minor and
major releases, and every other channel, always get the plain notice.

### `cartographer setup`

The first-run path in one command (D253): the native service, the first KB and the agent
clients, then a health check. It adds no behaviour of its own — each step is the existing command
(`service install`/`start`, `kb create`/`kb clone` with `--restart`, `connect --no-input`) — and it
decides every step **before** running any:

| Step | Decided from | Already done when |
|---|---|---|
| server | `service status` | installed and running (installed but stopped → `service start`) |
| KB | `--remote`, `--no-remote`, or the interview; `git ls-remote <url>` | a KB in the data dir has that origin (trailing `/` and `.git` ignored) |
| agents | `--agents`, else every detected client | never skipped: `connect` is idempotent and re-syncs |
| verify | `/health` through `service status` | — |

- **The remote decides create vs clone.** An empty `ls-remote` answer means an empty repository:
  `kb create <name> --remote <url>`; any ref means content: `kb clone <url> <name>`. The name is
  `--name`, else the repository name. The same probe proves the remote is reachable with this
  machine's credentials, under the non-interactive git environment of `kb clone` (no prompts,
  SSH batch mode, host keys never auto-accepted — D173); a failure exits 2 **before anything is
  written**, with the remedy for the recognised git errors.
- **KB binding (D190).** `--kb` is passed through. Without it: one KB binds itself; agents that
  already carry a binding keep it; a KB this run adds next to existing ones is bound alone (the
  narrowest choice); otherwise, with two or more KBs and nothing to go on, an interactive run
  asks with the same picker as `connect` and a non-interactive one exits 2 naming `--kb`.
- **Refusals, all before any change:** no `git` on `PATH`; a client already pointed at a
  non-loopback server (setup provisions a local one — `connect` is the command for a remote
  server); no remote and no mounted KB in a non-interactive run; no agent detected and none named.
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

The install scripts and the Homebrew cask end a first install on `Next: cartographer setup`, and
the dashboard shows the same hint on its `next` line while a loopback server has no service
behind it or answers with no KB mounted.

### `cartographer agents`

Lists the supported providers, whether they are installed on the machine and whether they are
connected (present in the machine-wide `.cartographer.yaml`, `~/.cartographer.yaml`).

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
4. `provider-root` — for a provider with a root of its own, that root (`$HERMES_HOME`);
5. `app-dir` — an application-installation directory declared for this GOOS. Only a confirmed location is
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
on the machine now, while `config-dir`, `env-config-dir`, `provider-root` and `app-dir` are
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
hooks); `connect`/`sync` print an info line for each one.

**Multi-KB servers (D92).** `connect` reads `GET /health` before emitting MCP
configuration. With one mounted KB (or an older single-KB server that omits
`kbs`) it keeps the compatible single entry, `<server_name>`, pointed at the
bare `/mcp` URL. With two or more KBs it writes one entry per KB, named
`<server_name>-<kb>` and pointed at `/mcp?kb=<kb>`, and records that KB list in
`.cartographer.yaml` (`known_kbs`). `sync` repeats the enumeration: it adds new
entries, removes entries for disappeared KBs, and performs the bare↔suffixed
rename on one-to-many transitions. If the server cannot be reached, it leaves
the MCP entries and `known_kbs` untouched and warns; run `sync` again once it is
up.

**Kiro subagents (D195).** Kiro receives KB subagents as JSON configs in
`~/.kiro/agents/` (and `.kiro/agents/` in workspace scope), which `kiro-cli agent
list` reports as Global/Workspace and the built-in agent delegates to through its
`use_subagent` tool, selecting by `description`. Its `hook` cell stays
unsupported — the shipped client has no hook mechanism, so its re-sync trigger
remains the scheduled timer (`interoperability.md` §Kiro hooks).

**Workspace scope (D193).** `cartographer workspace bind <provider> <path> --kb <name>…` moves a
provider from one machine-wide catalogue to one projection per bound repository: the KBs land in
that repository's own project-local directories and nothing KB-sourced is written under `$HOME` any
more. `connect --workspace <repo>` makes the same choice at connect time, which is the only moment
it is free — a provider-global connect materializes every selected KB into `$HOME` first, and moving
them afterwards is a migration. `workspace unbind` removes the declaration and the next `sync`
prunes what was projected there. `workspace list` shows the bindings, with `[gone]` next to a
directory that is not there any more.

Existing configurations are untouched: the scope is per provider, absent means the historical global
catalogue, and no upgrade changes it. Full rules, the project-local destination matrix, the
repository-hygiene guarantees and the providers that cannot be scoped at all → `sync.md`
§Workspace scope.

**Routed servers (D187).** When `/health` reports `mount_mode: routed`, the KB is no longer part of
the URL: the client writes **one** entry, `<server_name>`, pointed at the path the server names in
`routed_path` (`/mcp/routed`), whatever the provider is bound to. The binding still decides which
KBs that provider may use — routing changes the transport, not the authorization — and the
generated instructions block names the `kb` value each KB's tools must be called with. Both facts
are persisted in `.cartographer.yaml` (`server_mount_mode`, `server_routed_path`) so `doctor` and
`status` can derive the expected entries offline.

Switching an existing deployment between the two modes is a **reconnect**, not a silent rewrite: it
changes the *shape* of every entry, which an incremental sync cannot see. `cartographer status`
reports `mount mode changed: …` and names `cartographer reconnect`, the same answer D142 gives to a
server-version change. The removal set covers both shapes, so a reconnect leaves no orphan entry
from the previous mode.

**Kiro and flat tool namespaces (D102).** Kiro's MCP tool namespace is flat across servers, unlike
Claude Code/Codex/OpenCode which namespace per server: writing 2+ MCP entries for `kiro` (i.e.
connecting to a 2+-KB server) leaves only one KB's tools reachable in a Kiro session unless the
*server* mounts the others with a `tool_prefix` (`docs/deployment.md` §MCP tool-name prefix, D102).
`connect`/`sync` warn on stderr in that case; the operator is expected to add
`tool_prefix`/`tool_prefix_mode` server-side. The warning stays **silent against a routed server**:
one entry cannot collide with itself, and routing is the other answer to the same problem. Since D120 `/health` advertises each KB's effective
`tool_prefix`, so the client can see which KBs are already namespaced instead of reasoning from the
precondition alone.

**Antigravity and the 64-character tool identifier (D201).** Antigravity shows each tool as
`mcp_<server>_<tool>` and drops any identifier over 64 characters, silently. `connect`/`sync`
compute the longest identifier each Antigravity entry would produce — entry name, the KB's effective
`tool_prefix` from `/health`, the longest tool name — and warn on stderr when it exceeds the limit,
naming the entry. The remedies are server-side: a shorter `kbs[].tool_prefix`, or
`mcp.mount_mode: routed`, whose single entry carries unprefixed tools. Without `/health` facts the
check stays silent.

**Prefix discovery (D120).** Every client-owned direct tool call — manifest pull during `sync`,
remote `reindex`, the TUI's status probes — qualifies the tool name with the prefix the server
advertises for that KB in `/health`, never with one re-derived locally from the KB name. A locally
derived prefix is a guess: `tool_prefix` is an arbitrary operator string, so a client that guessed it
called tools that did not exist and reported the resulting failure as an unreachable server. The
discovered value is used live and never persisted, so changing a prefix server-side needs no
client-side reconnect.

```bash
cartographer connect                                   # all agents detected on the machine
cartographer connect claude                             # Claude Code only
cartographer connect --agents claude,codex              # selected subset
cartographer connect opencode --server-url http://cartographer.example.com/mcp --auth
cartographer connect claude --pin-key homelab=0123...  # pin a KB Ed25519 public key
cartographer connect all --auto-trust --dry-run
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `hermes` \| `antigravity` \| `all` (all detected agents) |
| `--agents` | *(unset)* | Comma-separated subset (`claude,codex`); cannot be combined with the positional provider |
| `--kb` | *(unset)* | Which KBs this client may receive (repeatable, or comma-separated; `all` for every mounted KB). Required on a **first** connect against a server mounting two or more KBs — see below (D190) |
| `--server-url` | `http://127.0.0.1:39273/mcp` | Cartographer server URL |
| `--auth` | `false` | Enables the Bearer header in generated configs |
| `--token-env` | `CARTOGRAPHER_TOKENS` | Env var holding the Bearer token |
| `--dry-run` | `false` | Prints what would be written, in the conditional (`would write`, `would connect`), and writes nothing ([D147](decisions/D147-every-reported-write-is-observed-never-intended.md)) |
| `--auto-trust` | `false` | Also treats KB skills as trusted (unsigned) |
| `--pin-key` | *(repeatable)* | Pins `KB=PUBLIC_KEY` for Ed25519-verified provisioning artifacts; existing pins are preserved |

`signing_keys` in `.cartographer.yaml` stores public-key pins per KB. Pins are
operator-supplied and are never learned from `sync_pull`; multiple pins permit
key rotation. Pin the new key, switch the server signer, then remove the old
pin after all clients have synchronized.

If no provider is detected and no explicit name is passed, the command exits with an error
(exit 1) without writing anything.

**Interactive form (TTY, D49+D64+D86).** With no form flags and in a TTY, the form shared with the
TUI opens (`connectform.go`): each field shows a contextual hint below it when focused
("Token env var" is the **name** of the environment variable holding the bearer token — the token
itself is never written to disk; with Auth off the field is rendered secondary and the hint says it is
ignored). In the standalone `connect` form, the four provider checkboxes are pre-selected from the
installed-agent set; select one or more with Space or Enter. The Server URL prefill follows the precedence existing `.cartographer.yaml` >
`CARTOGRAPHER_SERVER_URL` (client env) > `http://127.0.0.1:39273/mcp` — the loopback literal the local service listens on, never `localhost`, which Windows resolves to `::1` first, where nothing listens (D231). A `.cartographer.yaml` still carrying the old default `http://localhost:39273/...` is read back on `127.0.0.1` (path kept), so the next `connect` or `sync` rewrites every client's MCP entry; any other URL is left as written. On submit a **probe** runs
(`client.Health`, `GET /health`, 5s timeout, token from env only if Auth is enabled) before writing
any file: a reachable server with no mounted KB explains the `kb create` then service-restart path;
otherwise on failure the form is re-shown with the entered values and an inline error
(distinguishing a 401 "token rejected" from a network error), with an override available — in CLI a
`y/N` prompt "proceed anyway?", in the TUI a second consecutive Connect with no changes forces the
connection. A failed `doConnect` also re-shows the form populated (connect is idempotent: no
`disconnect` is needed to retry).

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
in `.cartographer.yaml` — it surgically removes every managed MCP server entry (the bare name and
any persisted per-KB suffixed names) from that provider's
config file (`internal/configurator.Remove`, the inverse non-destructive merge: the rest of the
file is left intact; if the `mcpServers`/`mcp` map ends up empty it is not deleted), prunes the
managed artifacts registered for that provider in the lockfile (`provisioning.PruneManaged` — only
managed files, never untracked ones), then removes the provider from the lockfile and from
`.cartographer.yaml`. If the lockfile ends up with no providers it is removed; `.cartographer.yaml`,
on the other hand, is **never deleted** (D64): with zero agents it stays on disk with `agents: []`,
preserving `server_url`/`server_name`/`auth`/`token_env`/`trust`/`known_kbs`/`clients` as defaults for the next
`connect` (a disconnect→connect restarts from the previous server, not from `http://127.0.0.1:39273/mcp`).

```bash
cartographer disconnect                # every connected provider
cartographer disconnect claude         # Claude Code only
cartographer disconnect --agents claude,codex # selected subset
cartographer disconnect all --dry-run  # preview without writing
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `hermes` \| `antigravity` \| `all` (every connected provider) |
| `--agents` | *(unset)* | Comma-separated subset (`claude,codex`); cannot be combined with the positional provider |
| `--dry-run` | `false` | Prints without removing |

Idempotent: exit 0 even if there was nothing to remove (no `.cartographer.yaml`, provider
already disconnected, MCP entry already absent, ...). Exit 2 only on an actual error (I/O, malformed
provider config JSON, ...).

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
for the MCP-specific approval states). MCP artifacts in `needs_approval`/`approval_stale` get their own
`cartographer approve mcp <name> --kb <kb>` hint, separate from the `--auto-trust` suggestion for
the other kinds. Before the artifact report it prints the
client and server versions. A non-`dev` mismatch is a warning only (it does not change the exit
code); on loopback, an installed local service also gets a `cartographer upgrade-repair` hint
(D121: it replaces the running service **and** re-syncs the providers in place, where the former
`service restart` hint only did the first half).
A newer release known to the update cache adds `update available: vX (installed vY) — <command>`
(JSON `update: {latest, kind, channel, command}`, present only then), and a remote server that
reports one in `/health` adds `server update available: vX` — the server operator's job, so no
command. Both read local state only (the cache is refreshed by the session hook and `update
check`) and neither changes the exit code (D254).
For an unavailable endpoint, the table names the configured endpoint once and
suggests checking that URL (or `cartographer service status` for loopback);
connected providers are reported as `unknown`, rather than repeating a network
failure for each provider.

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
cartographer sync [--client <provider>]... [--auto-trust] [--dry-run] [--no-heal]
```

| Flag | Default | Effect |
|---|---|---|
| `--client` | *(all)* | Syncs only this provider (repeatable). A provider left out is not touched at all: not its MCP entries, not its artifacts, not its lockfile entry |
| `--dry-run` | `false` | Prints without writing |
| `--auto-trust` | `false` | Also treats KB skills as trusted (unsigned) |
| `--no-heal` | `false` | Reports managed artifacts that diverged on disk instead of restoring them from the server (D139) |

Each provider receives only the KBs bound to it (§`cartographer client`), so the
manifest — and therefore the revision — differs per provider. When every targeted
provider agrees, `sync` prints one `synced to revision <r>` line as before;
when bindings made them diverge it prints one line per provider instead, rather
than implying an agreement that does not exist. See [`sync.md`](sync.md)
§Per-provider projection.

Before anything else — before the client-state lock and before the first network call, `--dry-run`
included — `sync` checks the environment variables the targeted providers need: each provider's own
base-directory variable (§Hermes Agent, `$HERMES_HOME`) and, when `.cartographer.yaml` has `auth: true` with a
`token_env`, that variable. Missing ones are reported **together, in one error naming each**, so an
unattended run (session-start hook, sync timer, CI — none of which inherit a login shell's exports)
diagnoses its whole misconfiguration in a single run
([D222](decisions/D222-sync-checks-the-environment-once-and-the-401-names.md)). The check is
read-only and never contacts the server: it verifies a token *exists*, not that the server accepts
it — a token that is set but wrong is still a 401 from `sync_pull`. When nothing is missing it
prints nothing. A provider left out by `--client` is not checked.

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
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `hermes` \| `antigravity` \| `all` (every connected provider) |
| `--agents` | *(unset)* | Comma-separated subset; cannot be combined with the positional provider |
| `--dry-run` | `false` | Both halves simulate, nothing is written |

**When to prefer it over `sync`.** Pruning is managed-only, so anything an *older Cartographer
version* wrote under a different name — a generated plugin whose filename changed, a managed block
whose marker spelling changed, a hook registered outside the block — is not in the current managed
set and survives every sync. The connect half writes the current shape from nothing, which is what
removes them. For everything else, `sync` is the right tool and stays incremental.

**What it preserves.** Everything in `.cartographer.yaml`: server URL and name, auth mode and token
env, trust, pinned signing keys, MCP approvals, search roots and paths. It is a rebuild, not a
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
refresh. Every finding names a real path on this machine and the command that fixes it (`sync`,
`reconnect`, `connect`, `service sync-timer install`), because a diagnosis nobody can act on is
noise and a doctor that silently fixes things is a doctor nobody can predict.

The checks:

| Check | What it looks at |
|---|---|
| `client-config` | `.cartographer.yaml` exists and parses; an agent is connected; every configured provider is still installed |
| `lockfile` | present, readable, and in the v2 format — a v1 file on disk is migrated *in memory* by every read, but stays v1 until something rewrites it |
| `managed-files` | the on-disk verification of D139, per provider: `missing`, `modified`, `unregistered`; plus files sitting inside a managed skill/hook directory that no lock entry accounts for (D178) — reported only, since doctor cannot prove Cartographer wrote them |
| `mcp-entries` | the Cartographer entries in the provider's native config match the KBs recorded in `.cartographer.yaml` — an entry for a KB the server no longer mounts, or a missing one |
| `instructions` | exactly one well-formed managed block per provider that has instructions materialized (begin recognized by prefix, so a block written by an older version still counts), **and** that the provider actually reads the file it was written into (D189) |
| `hooks` | one native registration per managed hook — the D99 double-fire is a registration left outside the managed block by Codex's own rewrite |
| `server` | `/health` reachable; the recorded `server_version` (D142) against the live one; client binary against server. When an unreachable server is loopback **and** no local native service is installed, the finding names that cause and the two remedies instead of pointing at `service status`, which would only repeat `installed: false` (D174) |
| `trigger` | every connected provider has a session hook, or the scheduled trigger is installed (D140) |
| `capability` | every per-KB gate the server advertises on `/health` is on, and no KB was mounted by discovery rather than by a `kbs[]` entry (D151). Info severity: it names the setting that would change it |
| `symlink` | no managed destination directory is a symlink, or anything else that is not a plain directory — provisioning refuses to write through one, so the artifacts it would hold are not installed (D148, widened in D216) |
| `kb-collisions` | no two KBs bound to the same provider claim one `kind`+`name` (D171). `sync` refuses outright when they do, so a machine that has not synced since the binding changed would otherwise show no symptom. Silent when the server is unreachable |
| `update_available` | a newer release is in the update cache (D254). Info severity; the fix is the channel's upgrade command. Cache only, so doctor stays offline |
| `unbound-residue` | no managed file comes from a KB no longer bound to the provider holding it (D170) — a projection predating an unbind, or a hand-edited lockfile. Only for providers with an explicit binding; a file with no recorded source (a lockfile written before D170) is unknown, not wrong, and never reported |

**Severities.** `error` — something is broken now (a managed file missing, a hook firing twice);
`warning` — something is stale or suboptimal (v1 lockfile, no trigger for a hook-less provider, a
version difference); `info` — context that cannot be acted on by itself and never changes the exit
code (managed entries recorded before content hashes existed, which nothing can verify). Findings
are printed errors first.

**`doctor --repair-hashes`** re-records the materialized hash of every managed entry that has none, computing it from the bytes already on disk (D157). It exists because the previous remedy for that narrow gap was `reconnect`, which prunes and rewrites **every** managed artifact on every client — in the field ~150 file operations to backfill six hashes, with a partial failure leaving both clients without skills. A backfilled entry is marked `adopted_at` in the lockfile: it is **adopted, not verified** — nothing was compared against the server, so drift is detectable only from the next server-side change onward. An entry whose file is *missing* is left alone: that is real drift for `sync` to fix, not something to paper over.

**Exit codes**: `0` clean, `1` findings (error or warning), `2` an error running the command — the
same convention `status` uses. An unreachable server is one `warning` finding, not a failure:
`doctor` stays useful offline.

**JSON shape**: `{schema_version, error_count, warning_count, info_count, findings[]}`, each finding
`{check, severity, message, path, fix}`. Flat and stable — it ends up in someone's monitoring.

The bootstrap hook and the scheduled timer deliberately do **not** run it: it is an operator command,
and eight checks on every session start is exactly the background cost D60 avoided by keeping
the bootstrap script silent and deterministic.

### `cartographer service sync-timer <action>`

The scheduled sync trigger for clients with no session-start hook (D140) — distinct from the
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
installed is a success. The timer runs `cartographer sync` **without** `--auto-trust`: an
unattended job must not grant a trust the user never gave, while the persisted `trust` setting
still applies. `connect` and `status` name this command once per invocation when a connected
provider has no session hook — they never install it.

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

Plain `restart` keeps its previous behavior. `restart --wait` gracefully replaces the process
(`SIGTERM`, so in-flight requests drain) and only prints success once `/health` proves the
installed binary version is serving; `--config` selects the config used for that verification,
and is otherwise unnecessary because the installed service definition is discoverable.

Windows takes the same three verbs to a different scheduler, so two behaviours are worth naming.
`stop` also **disables** the task, because its logon trigger and its restart-on-failure would
otherwise bring the server straight back; `start` and `restart` re-enable it first, which is why a
service stopped on purpose stays stopped and a restart after a stop still works. And the graceful
replacement is not a signal but a named event in the user's session, followed by an explicit
relaunch of the task — nothing else would restart a process that drained and exited cleanly.

Operational details (generated paths, defaults, behavior with an existing config, automatic
repair on `install.sh update` and Cask upgrade) in `deployment.md` §Example: native local service
and §Upgrades, schema migration, and repo growth.

### `cartographer import`

A mechanical import scaffold (D74 WP2), a sibling of the agentic `kb-import` skill
(`internal/skillbundle/bundled/kb-import/`): unlike the other subcommands, it doesn't talk to the
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
| `--default-map` | `""` | Default map for source directories with no `--map` (D77: used to be `--archive`) |
| `--map` | *(repeatable)* | Per-directory mapping `<srcdir>=<map>` (`srcdir` relative to `--source`, `.` for the root) |
| `--dry-run` | `false` | Prints the mapping plan (source → concept id) without writing |
| `--commit` | `false` | Makes one final commit containing only import-written paths; pre-existing dirty work is untouched |
| `--message` | `import: <source> -> <kb>` | Commit message; implies `--commit` |
| `--dir-as-concept` | `false` | Promotes a source directory with `index.md` (or `README.md`) into an expanded concept and keeps its satellites together |

**`--map` covers a subtree.** Resolution is longest matching prefix, then `--default-map`, then the
unmapped error (D162): `--map a/b=m` covers `a/b`, `a/b/c` and below, a more specific `--map a/b/c=n`
wins for its own subtree, and matching is at **segment boundaries**, so `a/b` never covers `a/bc`. `.`
is a legal source and covers everything. Before this the lookup was keyed on the exact directory, so a
corpus with 58 source directories needed 58 flags — the only choices were one map for everything or one
flag per directory, with nothing in between.

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
from outside the server, so the server's FTS index knows nothing about it — `search` returned zero
results while `concept_list` saw everything, and the natural conclusion was that the import had
failed. On completion the command rebuilds the index when a configured server is reachable, and
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
paths" table materialized in the instructions block (`docs/sync.md` §Path portability placeholders), as well
as a standalone debugging tool.

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
another machine. Before this the root was silently skipped and the only message on offer was the
one below, which talks about directory depth — so a typo in a root looked like a `search_depth`
problem ([D216](decisions/D216-the-client-half-reaches-parity-on-windows.md)). The same warnings
surface in `AppliedResult.Warnings` when a `{{repo:…}}` placeholder is resolved during `sync`.

Exit code: `0` resolved (path on stdout), `1` not resolved (no `paths:` entry, no clone
found under `search_roots`, or an ambiguous key across several distinct remotes — error message on
stderr with the full form to use), `2` usage error (missing argument or not in the
`repo:...`/`path:...` form).

## Adding a provider

Every supported provider is one descriptor in `internal/configurator/registry.go`
([D137](decisions/D137-declarative-provider-registry-two-tables-owned-by-the.md)): its `Provider` constant and wire value, display
name, native MCP config file and format (`FormatJSON` with its server key, or `FormatTOMLBlock`),
whether that file may be deleted once emptied (never for Claude Code — `.claude.json` is Claude's
own shared state), whether it can carry MCP auth headers, whether its MCP tool namespace is flat
across servers, the detection evidence (binary names, config directories in probe order, optional
env-anchored config directories, optional per-GOOS application directories), and its emitter
function.

Two orders are exposed and both are user-visible: `Providers()` — the order `EmitAll` and the
client subcommands iterate — and `DetectionOrder()`, the order `cartographer agents` and the TUI
list agents in.

Adding a provider therefore means: one descriptor, one emitter (provider output formats genuinely
differ, so that stays code), its cells in the kind × provider matrix (`internal/provisioning`, see
[`sync.md`](sync.md) §Kind × provider matrix), and — if it has a native hook mechanism — one entry
in `hookMechanisms`. A missing matrix cell fails a completeness test; nothing else needs editing.
A provider whose MCP configuration Cartographer does not own declares neither a config file nor an
emitter and is skipped by `connect`/`disconnect` (`ManagesMCPConfig`); one that materializes outside
the shared base dir declares `BaseDirEnv` instead ([D141](decisions/D141-hermes-is-a-supported-provider-that-receives.md)).

### Hermes Agent

`cartographer connect hermes` registers Hermes for **artifact delivery only**. It writes no MCP
configuration: Hermes' endpoint list lives in a `config.yaml` rendered by its Ansible role and
recreated on the next playbook run, so anything written there would be lost — `connect` says so
explicitly rather than silently doing nothing, and pointing Hermes at the server stays the
operator's job. The output is scoped to match: no MCP-entry line is printed, and the closing
"restart the … sessions to load the MCP tools" hint names only the providers that received one
([D147](decisions/D147-every-reported-write-is-observed-never-intended.md)). For the same reason Hermes is absent from the interactive connect form, which offers
the providers whose MCP configuration `connect` writes.

- **`$HERMES_HOME` is required**: it is the base dir artifacts are materialized under, recorded as
  `base_dir` in that provider's lockfile entry. Unset, `connect hermes` fails naming the variable
  instead of writing into the home directory, where the agent would never look.
- **Only `skill` is supported**, and it is *delivered* to `skill-inbox/<name>/cartographer/` rather
  than installed — adoption is the agent's own decision, via `skill_manage`. Nothing is ever written
  under `$HERMES_HOME/skills/`. See [`sync.md`](sync.md) §Hermes.
- **The trigger is the scheduled timer** (`cartographer service sync-timer install`): Hermes has no
  session hook, so nothing fires at conversation start.
- `disconnect hermes` prunes the delivered inbox directories and drops the provider from
  `.cartographer.yaml`; it touches nothing else.

## Files generated per provider (HTTP transport)

| Provider | Generated file | Key |
|----------|--------------|--------|
| Claude Code | `.claude.json` | `mcpServers` (JSON) |
| Codex CLI | `.codex/config.toml` | managed block `[mcp_servers.cartographer]` (TOML, marker `cartographer:mcp:*`) |
| Kiro | `.kiro/settings/mcp.json` | `mcpServers` (JSON) |
| OpenCode | `opencode.json` | `mcp` (JSON) |
| Google Antigravity | `.gemini/config/mcp_config.json` | `mcpServers` (JSON) |
| Crush | `.config/crush/crush.json` | `mcp` (JSON) |
| Hermes Agent | none — see below | — |

KB-provided stdio descriptors (D116) share these same files with per-name ownership. Claude Code,
Codex and Kiro receive native `command`, `args` and `env` fields (Kiro also keeps `autoApprove: []`);
OpenCode uses `type: "local"`, an ordered command array and `environment` with `{env:VAR}` references;
Crush uses `type: "stdio"` with a string `command` plus a separate `args` array.
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
> may require `mcp-remote`/`mcp-auth.json`; see `docs/interoperability.md` §Known risks.

**Google Antigravity** — with auth (Antigravity natively resolves `${VAR}` in headers):
```json
{
  "mcpServers": {
    "cartographer": {
      "serverUrl": "http://127.0.0.1:39273/mcp",
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
Crush also has no documented user-level subagent directory and no hook mechanism, so those two
artifact kinds are unsupported for it and its bootstrap trigger is the scheduled timer.

The six formats above are generated from the same provider-neutral core,
`configurator.EmitServer(name, spec ServerSpec, provider)` (D69): `Emit(cfg, provider)` is a
thin wrapper around `EmitServer(cfg.Name, cfg.toSpec(), provider)`. The same `EmitServer` is
reused by `internal/provisioning` to materialize the third-party MCP servers a KB
distributes (`mcp/<name>.json`, kind `mcp`) — not Cartographer's own entry, but any
server, with per-name ownership in the same file (`mcpServers.<name>`/`mcp.<name>`/block
`[mcp_servers.<name>]` marked `# cartographer:mcp:<name>:begin/end`). Details →
`docs/sync.md` §MCP servers.

## `.cartographer.yaml`

### MCP descriptor approval

Unsigned third-party MCP descriptors need a separate, local approval even when
`trust` or `--auto-trust` is enabled. Inspect and record the current descriptor
with `cartographer approve mcp <name> --kb <kb> --yes` (interactive terminals
default to no confirmation). Approval records the source KB, artifact name,
content hash and timestamp in `.cartographer.yaml`; a content change requires
reapproval. `cartographer approve revoke mcp <name> --kb <kb>` is idempotent
and takes effect on the next `cartographer sync`, which prunes managed provider
config.

Written/updated by `connect` in the user's home directory (`~/.cartographer.yaml`, machine-wide —
`clientconfig.TargetDir`, D52): it records which server the machine is connected to and which
providers are connected. One file per machine, not per project: this avoids drift with a provider
connected in one repo but not another.

```yaml
server_url: http://127.0.0.1:39273/mcp
server_name: cartographer  # name under which the server is registered in the MCP configs (no longer a flag: always "cartographer", override only by editing this file)
auth: false
token_env: CARTOGRAPHER_TOKENS
agents: [claude, opencode]
known_kbs: []    # mounted KB names discovered by connect/sync; empty = bare single-KB endpoint
clients:         # per-provider KB binding (D169); absent provider = every known KB
  claude:
    kbs: [homelab]
search_roots: ["~/Documents"]   # where repoindex.Scan looks for git clones for {{repo:<key>}} (D75)
search_depth: 4                 # how many levels repoindex descends from each root (D162); omitted when 0 = the default
paths: {}                       # manual name -> path mapping for {{path:<name>}} (and an override for {{repo:<key>}}, D75)
update:                         # D254; omitted entirely when both are the default
  check: true                   # false: no update lookup at all
  policy: notify                # notify | auto-patch (patch releases via homebrew/install.sh/install.ps1 install themselves)
```

An unknown `update.policy` makes the file fail to load, naming the valid values: it is the one
setting that lets a machine upgrade itself.

`known_kbs` is server-owned: `connect` and `sync` overwrite it wholesale with
what `/health` advertises. `clients` is user-owned and is never written by them
— it is maintained with `cartographer client` (below). The legacy `kbs` key
written before D169 is still read and is migrated to `known_kbs` on the next
write.

### `cartographer client`

Declares which Knowledge Bases each connected provider may receive. Every
subcommand works offline: it reads and writes `.cartographer.yaml` only and
never contacts the server, so a KB name that is not currently advertised is a
warning, not a failure.

| Command | Effect |
|---|---|
| `client list` | one row per connected provider: resolved KBs and the origin of the answer (`explicit` / `default (all known)`) |
| `client show <provider>` | that provider's bound KBs, its origin, and the known KBs it is **not** bound to |
| `client bind <provider> <kb>[,<kb>...]` | adds; creating the first binding narrows the provider from "every known KB" to only those listed, and the output says so |
| `client unbind <provider> <kb>[,<kb>...]` | removes; removing the last KB leaves the provider bound to **no** KBs |
| `client reset <provider>` | deletes the binding, returning the provider to the default |
| `client update [--check=true\|false] [--policy notify\|auto-patch]` | shows, or sets, the client-wide `update:` block (D254) |

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
    "claude": { "applied_revision": "sha256:…", "server_version": "1.4.0", "managed": [ /* ManagedFile[] */ ] },
    "opencode": { "applied_revision": "sha256:…", "server_version": "1.4.0", "managed": [ /* ManagedFile[] */ ] }
  }
}
```

The old v1 format (`{"applied_revision", "provider", "managed"}`, single provider) is
automatically migrated on read (`provisioning.ReadLockFile`) into `{"providers": {<provider>:
{...}}}`. See `docs/sync.md` §Client lockfile for the full model (drift, pruning).

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
| `enter` / `s` | Connect (if not connected) or resync the **selected** provider |
| `S` | Sync every connected provider, one at a time under a single client lock (D172) |
| `d` | Disconnect (if connected) — opens an inline `y`/`n` confirmation naming its KBs |
| `r` | Refresh status |
| `q` / `Esc` | Quit |

`d` on a connected agent opens a confirmation screen (`y` confirms, any other key,
including `n`/`Esc`, cancels and returns to the list); on a non-connected agent it's a no-op. The TUI is a
subset of the CLI: it uses the same `doConnect`/`doDisconnect` logic as `cartographer
connect`/`disconnect`, but doesn't expose `--dry-run`/`--auto-trust`. Outside a TTY, `cartographer`
with no arguments prints the usage (like `cartographer help`).

## Non-destructive merge

If a config file already exists, `connect` reads the existing content, adds or
updates only the server's key (`server_name`, default `cartographer`) and rewrites the file. Other configuration present
in the file (other MCP servers, other keys) is left untouched. `disconnect` performs the
inverse operation (`configurator.Remove`): removes only the server's key from the `mcpServers`/`mcp` map and
rewrites the file, leaving everything else intact (other MCP servers, other top-level keys).

## Installation

One channel per platform, and an upgrade uses the one the binary came from:

```bash
brew install beppetemp/tap/cartographer     # macOS (cask from the tap)
irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex   # Windows (PowerShell, D252)
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
```

`install.sh` downloads the latest binary from the GitHub Release for the current platform (darwin/linux ×
amd64/arm64), verifies the checksum if `sha256sums.txt` is present in the release, and installs it into
`/usr/local/bin` (or `~/.local/bin` if not writable). Also supports `update` and `uninstall` as the
first argument. Run from a Windows shell (Git Bash, MSYS2, Cygwin) it refuses and prints the `install.ps1`
command instead. `install.ps1` is the Windows counterpart (install/`update`/`uninstall`, per-user,
checksum-verified). See `docs/deployment.md` §Client installation.
