# Multi-provider client

The `cartographer` binary bundles, besides the server (`serve`), the client subcommands that
connect a machine to a Cartographer server: `agents` (discovery), `connect`
(configures + materializes provisioning artifacts — skills, agents, hooks, instructions,
mcp — D69), `disconnect` (disconnects, the inverse of `connect`), `status` (drift, with per-kind
counts), `sync` (realigns), `resolve` (resolves a `{{repo:}}`/`{{path:}}` placeholder, D75), plus
a **TUI dashboard** when invoked with no arguments in a terminal.

The client **always talks to the server over HTTP** (the `sync_pull` tool): there is no
stdio transport on the client side, nor a separate binary — see `decisions.md` for the
rationale. Generating the MCP config files (`internal/configurator`) and the
materialization logic (`internal/provisioning`) are the same used server-side by
`sync_check`/`sync_apply` (`docs/sync.md`).

## Subcommands

### `cartographer agents`

Lists the four supported providers, whether they are installed on the machine (`internal/agents.Detect`:
binary in PATH or a known config directory) and whether they are connected (present in the
machine-wide `.cartographer.yaml`, `~/.cartographer.yaml`).

```bash
cartographer agents
```

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
`.cartographer.yaml`. `sync` repeats the enumeration: it adds new entries,
removes entries for disappeared KBs, and performs the bare↔suffixed rename on
one-to-many transitions. If the server cannot be reached, it leaves the MCP
entries and `kbs` list untouched and warns; run `sync` again once it is up.

```bash
cartographer connect                                   # all agents detected on the machine
cartographer connect claude                             # Claude Code only
cartographer connect --agents claude,codex              # selected subset
cartographer connect opencode --server-url http://cartographer.example.com/mcp --auth
cartographer connect all --auto-trust --dry-run
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `all` (all detected agents) |
| `--agents` | *(unset)* | Comma-separated subset (`claude,codex`); cannot be combined with the positional provider |
| `--server-url` | `http://localhost:8080/mcp` | Cartographer server URL |
| `--auth` | `false` | Enables the Bearer header in generated configs |
| `--token-env` | `CARTOGRAPHER_TOKENS` | Env var holding the Bearer token |
| `--dry-run` | `false` | Prints without writing |
| `--auto-trust` | `false` | Also treats KB skills as trusted (unsigned) |

If no provider is detected and no explicit name is passed, the command exits with an error
(exit 1) without writing anything.

**Interactive form (TTY, D49+D64+D86).** With no form flags and in a TTY, the form shared with the
TUI opens (`connectform.go`): each field shows a contextual hint below it when focused
("Token env var" is the **name** of the environment variable holding the bearer token — the token
itself is never written to disk; with Auth off the field is rendered secondary and the hint says it is
ignored). In the standalone `connect` form, the four provider checkboxes are pre-selected from the
installed-agent set; select one or more with Space or Enter. The Server URL prefill follows the precedence existing `.cartographer.yaml` >
`CARTOGRAPHER_SERVER_URL` (client env) > `http://localhost:8080/mcp`. On submit a **probe** runs
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
`cartographer kb create <name>` followed by `cartographer service restart`. A successful connect
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
preserving `server_url`/`server_name`/`auth`/`token_env`/`trust`/`kbs` as defaults for the next
`connect` (a disconnect→connect restarts from the previous server, not from `http://localhost:8080/mcp`).

```bash
cartographer disconnect                # every connected provider
cartographer disconnect claude         # Claude Code only
cartographer disconnect --agents claude,codex # selected subset
cartographer disconnect all --dry-run  # preview without writing
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `all` (every connected provider) |
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
prints the diff (added/updated/removed, with `signed`).

### `cartographer sync`

Re-runs `sync_pull` and reapplies the manifest for every connected provider: materializes
add/update, prunes obsolete artifacts, updates the lockfile. Idempotent.

```bash
cartographer sync [--auto-trust] [--dry-run]
```

### `cartographer service <action>`

Manages the **server** as a native user service on the machine (local mode, D73):
launchd on macOS, systemd user unit on Linux. Client and server are the same binary: the
client subcommands talk to the daemonized server over loopback.

```bash
cartographer service install [--config <path>] [--data <dir>] [--http <addr>]
cartographer service uninstall|start|stop|restart
cartographer service status        # exit: 0 running, 3 installed but stopped, 4 not installed
```

Operational details (generated paths, defaults, behavior with an existing config, automatic
restart on `install.sh update`) in `deployment.md` §Example: native local service.

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

For every file: if it already has YAML frontmatter it's preserved, only adding missing fields;
otherwise it synthesizes the minimum — `title` from the body's first H1 (fallback: file name), `type: Note` if absent
(`WriteConcept` always requires it — a deviation from the original spec, see `decisions.md` §D74) — and
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

Exit code: `0` resolved (path on stdout), `1` not resolved (no `paths:` entry, no clone
found under `search_roots`, or an ambiguous key across several distinct remotes — error message on
stderr with the full form to use), `2` usage error (missing argument or not in the
`repo:...`/`path:...` form).

## Files generated per provider (HTTP transport)

| Provider | Generated file | Key |
|----------|--------------|--------|
| Claude Code | `.claude.json` | `mcpServers` (JSON) |
| Codex CLI | `.codex/config.toml` | managed block `[mcp_servers.cartographer]` (TOML, marker `cartographer:mcp:*`) |
| Kiro | `.kiro/settings/mcp.json` | `mcpServers` (JSON) |
| OpenCode | `opencode.json` | `mcp` (JSON) |

## Format of the generated files

**Claude Code** — with auth:
```json
{
  "mcpServers": {
    "cartographer": {
      "url": "http://localhost:8080/mcp",
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
url = "http://localhost:8080/mcp"
bearer_token_env_var = "CARTOGRAPHER_TOKENS"
# cartographer:mcp:end
```

**Kiro**:
```json
{
  "mcpServers": {
    "cartographer": {
      "url": "http://localhost:8080/mcp",
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
      "url": "http://localhost:8080/mcp",
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
      "url": "http://localhost:8080/mcp",
      "enabled": true,
      "headers": { "Authorization": "Bearer {env:CARTOGRAPHER_TOKENS}" }
    }
  }
}
```

> **Known risk**: OpenCode is SSE-first and support for custom headers on a remote MCP
> may require `mcp-remote`/`mcp-auth.json`; see `docs/interoperability.md` §Known risks.

The four formats above are generated from the same provider-neutral core,
`configurator.EmitServer(name, spec ServerSpec, provider)` (D69): `Emit(cfg, provider)` is a
thin wrapper around `EmitServer(cfg.Name, cfg.toSpec(), provider)`. The same `EmitServer` is
reused by `internal/provisioning` to materialize the third-party MCP servers a KB
distributes (`mcp/<name>.json`, kind `mcp`) — not Cartographer's own entry, but any
server, with per-name ownership in the same file (`mcpServers.<name>`/`mcp.<name>`/block
`[mcp_servers.<name>]` marked `# cartographer:mcp:<name>:begin/end`). Details →
`docs/sync.md` §MCP servers.

## `.cartographer.yaml`

Written/updated by `connect` in the user's home directory (`~/.cartographer.yaml`, machine-wide —
`clientconfig.TargetDir`, D52): it records which server the machine is connected to and which
providers are connected. One file per machine, not per project: this avoids drift with a provider
connected in one repo but not another.

```yaml
server_url: http://localhost:8080/mcp
server_name: cartographer  # name under which the server is registered in the MCP configs (no longer a flag: always "cartographer", override only by editing this file)
auth: false
token_env: CARTOGRAPHER_TOKENS
agents: [claude, opencode]
kbs: []          # mounted KB names discovered by connect/sync; empty = bare single-KB endpoint
search_roots: ["~/Documents"]   # where repoindex.Scan looks for git clones for {{repo:<key>}} (D75)
paths: {}                       # manual name -> path mapping for {{path:<name>}} (and an override for {{repo:<key>}}, D75)
```

`status` and `sync` read this file (via `internal/clientconfig`): without `.cartographer.yaml` they
fail with exit 2, suggesting `connect` first (`cartographer resolve` is the exception:
it works even without it, using `clientconfig.Default()`'s defaults).

## Lockfile v2 multi-provider

`.cartographer-sync.lock.json`, written by `connect`/`sync`, records for **each provider** what has
been materialized:

```jsonc
{
  "providers": {
    "claude": { "applied_revision": "sha256:…", "managed": [ /* ManagedFile[] */ ] },
    "opencode": { "applied_revision": "sha256:…", "managed": [ /* ManagedFile[] */ ] }
  }
}
```

The old v1 format (`{"applied_revision", "provider", "managed"}`, single provider) is
automatically migrated on read (`provisioning.ReadLockFile`) into `{"providers": {<provider>:
{...}}}`. See `docs/sync.md` §Client lockfile for the full model (drift, pruning).

## TUI mode (interactive dashboard)

Running `cartographer` with no arguments in a terminal opens an interactive dashboard
(`cmd/cartographer/tui.go`, `bubbletea`): it lists the four providers vertically, one card per
provider with an explicit status (`connected` / `not connected` / `not installed`) and indented
details below — binary path, MCP config, provisioning artifacts with per-kind counts
(`formatKindStatus`) — loaded asynchronously against the configured server.

Main keys:

| Key | Action |
|---|---|
| `enter` / `s` | Connect (if not connected) or resync |
| `d` | Disconnect (if connected) — opens an inline `y`/`n` confirmation |
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

```bash
brew install beppetemp/tap/cartographer     # preferred path on macOS (cask from the tap)
# or:
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
```

`install.sh` downloads the latest binary from the GitHub Release for the current platform (darwin/linux ×
amd64/arm64), verifies the checksum if `sha256sums.txt` is present in the release, and installs it into
`/usr/local/bin` (or `~/.local/bin` if not writable). Also supports `update` and `uninstall` as the
first argument. See `docs/deployment.md` §CI/CD and client installation.
