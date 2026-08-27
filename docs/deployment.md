# Deployment, operations, and disaster recovery

## Deployment and packaging

The server is distributed as a native **Go binary** (via `install.sh`, see §Client installation — client and server are the same binary). The Docker image only exists as a CI artifact for k8s deployment: **it is not a local deployment mode** (D73 — containers on macOS mean a slow virtualized filesystem for git/SQLite, and an always-on VM).

### Topologies

| Topology | Description |
|---|---|
| **Local** | The binary runs as a native user service (`cartographer service`, launchd/systemd); the client on the same machine points to `127.0.0.1`. |
| **Shared server** | HTTP with optional static bearer tokens for multiple agents, on a trusted network or behind a reverse proxy (for example Kubernetes). |

### Provisioning artifact signing

An explicit `kbs:` entry may set `artifact_signing_seed` to a 32-byte hex
Ed25519 seed. Mount it from a secret manager or protected secret volume: never
commit it, expose it in health/MCP output, or place it in logs. The server logs
only the KB and derived key ID. Distribute and pin the corresponding public key
on each client before switching a signer; unsigned KBs remain supported through
the explicit client trust policy.

### KB-provided MCP allow-list

Third-party MCP descriptors under `mcp/*.json` are denied by default, including
after an upgrade. To advertise one, add an exact per-KB `mcp_allowlist` entry
with its artifact name, `http` transport and normalized absolute target URL.
The server omits every unlisted descriptor from manifest and `artifact_list`
responses and logs the configuration action at startup; headers and environment
references never appear in that diagnostic. Stale allow-list entries are a
warning, so an endpoint can be staged before its descriptor is committed.
| **Partitioned servers** | Several instances mount different KBs. A client can connect to entries from more than one server. Do not use several active writer servers as replicas for the same KB; git conflict recovery is a safety net, not a write-scaling protocol. |

### State and volumes

The server process is **ephemeral** (k8s pod or local service); what persists on volume/disk:
- The KBs' git working trees.
- SQLite indices (optional, rebuildable).
- Optional local runtime files under `.cartographer/`. Search indexes are
  rebuildable; conflict registries are operational state and should survive a
  restart until resolved.

When `audit.log` is set, MCP tool execution appends an attempt+completion event
pair per call, so the file is a complete operational request log (D119). See
§Audit log below and [transport-auth](transport-auth.md) §Operational audit.

### Configuration: flags, env, YAML

`cartographer serve` resolves its configuration from three sources, in order of precedence (highest first):
**CLI flag > environment variable > YAML file (`--config`/`CARTOGRAPHER_CONFIG`) > default**
(`internal/config`, `config.Default()`). The YAML file is optional: without `--config`, only
defaults + env + flags apply, as in earlier versions.

Full annotated example: [`config.example.yaml`](https://github.com/BeppeTemp/cartographer/blob/main/config.example.yaml) (repo root).
Schema (`internal/config.Config`, YAML tags in parentheses):

```yaml
http: ":39273"                # (http) listen address; absent = stdio
init: true                    # (init) initialize missing KBs
auth:
  mode: "on"                  # (auth.mode) on | off | auto (default auto: on if tokens are present)
  tokens: [...]                # (auth.tokens) prefer env CARTOGRAPHER_TOKENS instead (secret)
  roles: [...]                 # (auth.roles) named fine-grained permission sets (D118), see below
data: /data                   # (data) directory with KB auto-discovery (one subfolder = one KB);
                              # paths already mounted explicitly (kbs[], including remote KBs cloned
                              # into this directory) are excluded from discovery — no double mount

kbs:                          # (kbs[]) explicit KBs, local path or remote git (bootstrap, see below)
  - remote: ssh://git@host:2222/user/wiki-kb.git
  - path: /data/kb-locale
  - remote: https://gitea.example.com/team/shared-kb.git
    name: shared-kb                                  # (kbs[].name) explicit name (D53): overrides
                                                      # the name derived from remote/path — the HTTP
                                                      # endpoint, token scope, clone dir, and the
                                                      # token_dir/age_key_dir conventions below all use
                                                      # this name
    author_name: "Team Bot"                          # (kbs[].author_name) commit author for this KB
    author_email: team-bot@example.com               # (kbs[].author_email)
    committer_name: "Cartographer CI"                # (kbs[].committer_name) optional, defaults to author
    committer_email: cartographer-ci@example.com     # (kbs[].committer_email)
    allow_artifact_write: true                       # (kbs[].allow_artifact_write) enables artifact_write/
                                                      # artifact_delete on this KB (default false, D71):
                                                      # writing a skill means injecting instructions that
                                                      # clients will execute — the capability must be
                                                      # granted per-KB by the operator; an rw token alone
                                                      # does not imply it
    tool_prefix: "team"                              # (kbs[].tool_prefix) opt-in per-KB tool-name prefix
                                                      # (default "", off): registers this KB's tools as
                                                      # `team__<tool>` instead of `<tool>`. Wins over
                                                      # `mcp.tool_prefix_mode` below. See §MCP tool-name prefix.
mcp:
  tool_prefix_mode: "off"       # (mcp.tool_prefix_mode) off (default) | kb-name: global default for
                                # every mounted KB that doesn't set its own kbs[].tool_prefix — kb-name
                                # derives the prefix from the KB's own name. See §MCP tool-name prefix.
git:
  autocommit: true            # (git.autocommit) commit after every write
  sync: true                  # (git.sync) read/write fetch/pull-rebase + post-write push if the KB has a remote
  ssh_key: /etc/kb-ssh/id_ed25519      # (git.ssh_key) default SSH identity for cloning kbs[] remotes
  known_hosts: /etc/kb-ssh/known_hosts # (git.known_hosts) host verification for the same clone
  author_name: "Team Bot"             # (git.author_name) default author (set with author_email)
  author_email: team-bot@example.com   # explicit configuration wins over Git config
  token_dir: /etc/kb-git-tokens         # (git.token_dir) directory <token_dir>/<name>.token (D53, see below)
  in_window: 30s                # (git.in_window) SyncIn freshness window: within this duration
                                # since the last successful fetch+pull, subsequent reads and writes skip it
                                # (no-op). Default 30s, 0 = sync on every write (D76)
  out_debounce: 3s              # (git.out_debounce) debounce of the per-KB async push after the
                                # commit: N writes in quick succession = 1 push. Default 3s, 0 = push
                                # synchronously inline, no worker (rollback flag, D76)
  # profile: server             # (git.profile) local (default) | server (D117): the fields below
  # base_branch: main           # become required. Protected review target, never directly pushed.
  # working_branch: cartographer/wiki  # optional; defaults to cartographer/<kb-name>
  # forge: github               # only supported forge today
  # github_owner: example-org   # must match the origin remote path exactly (HTTPS or SSH)
  # github_repository: wiki
  # github_api_url: https://api.github.com
  # github_token_env: CARTOGRAPHER_GITHUB_TOKEN  # the value stays in this process environment only
search:
  ollama_url: ""               # (search.ollama_url) enables semantic search
  ollama_model: nomic-embed-text
audit:
  log: ""                      # (audit.log) path to the audit log's JSONL file
  key_seed: ""                 # (audit.key_seed) Ed25519 hex seed for signing
  mode: best_effort            # (audit.mode) best_effort (default) | required
  max_segment_bytes: 0         # (audit.max_segment_bytes) rotate past this size; 0 = package default
  archive_dir: ""              # (audit.archive_dir) rotated segments; empty = beside the log
  retention_days: 0            # (audit.retention_days) delete checkpointed segments older than N days; 0 = keep
sops:
  age_key_dir: /etc/kb-sops-keys # (sops.age_key_dir) directory <age_key_dir>/<name>.age (D53, see below)
tools:
  profile: agent               # (tools.profile) agent (default: tools/list exposes only the core
                               # set for the LLM agent) | full (all tools, including governance/plumbing).
                               # Hidden tools remain callable via tools/call (D65).
```

`kbs[]` (YAML) is additive to `--kb`/`CARTOGRAPHER_KB` and `CARTOGRAPHER_KB_REMOTES` (append, not
replace) — all listed KBs get mounted. Scalar fields (`http`, `data`, tokens, ...)
instead follow flag > env > YAML precedence: the last explicitly set level wins.

### Bootstrapping a KB from a git remote (replaces the init container)

Every `kbs: [{remote: ...}]` entry (YAML) or `CARTOGRAPHER_KB_REMOTES` (env, CSV of URLs) is
cloned by `cartographer serve` at startup into `<data>/<name>` (name derived from the last segment
of the remote, without `.git` — or the explicit `kbs[].name`, if set, see D53 below), **only if
the destination doesn't already exist** — an existing clone is left untouched: the existing
`git-autocommit`/`git-sync` cycle handles fetch/push on subsequent writes
(`cmd/cartographer/bootstrap.go`). Requires `--data`/`CARTOGRAPHER_DATA`/`data:` as the destination
directory for clones.

`GIT_SSH_COMMAND` for the clone is built from `git.ssh_key`/`git.known_hosts` (`ssh -i <key>
-o UserKnownHostsFile=<known_hosts> -o StrictHostKeyChecking=yes`); if `GIT_SSH_COMMAND` is already
present in the environment, it wins and the YAML config is ignored. This removes the need for
a separate Kubernetes init container for the initial clone (see §K8s example).

**Per-KB git identity (D46)**: each `kbs[]` entry can override, for that one KB, the SSH key
(`ssh_key`/`known_hosts`) and the author/committer identity (`author_name`/`author_email`,
`committer_name`/`committer_email`) used by `serve` for the initial clone and for every commit
(`CommitOp`, conflict resolution). Fallback cascade: the KB's value → global `git.*` → the
repository/global Git identity (including Git's native ambient resolution) → hard-coded
`cartographer`/`cartographer@localhost` only when Git has no identity. Identity names and emails
must be configured as pairs; the default committer follows the resolved author
(the KB's own or the global one) if `committer_*` is not set. The per-KB environment assembled
this way **overrides** the server's process environment (the reverse of the "environment wins"
rule that applies to the global `git.ssh_key`/`GIT_SSH_COMMAND` above) — see
`internal/gitx.runGitEnv`. With the identity set via `kbs[].author_name`/`author_email`/`committer_*`
(or via global `git.author_name`/`author_email`), any `GIT_AUTHOR_*`/`GIT_COMMITTER_*` in the k8s
Deployment's `env:` become redundant and can be removed.

If your forge enforces author push rules, set a complete `author_name`/`author_email` pair to a
forge account. `service install` writes the same commented identity block into its generated
configuration. Existing placeholder-authored commits must be rewritten manually before the first
accepted push, because the forge validates every commit in that push.

**Explicit KB name, git token, and per-KB SOPS key by convention (D53)**: `kbs[].name` fixes the
KB's name (overrides the name derived from remote/path) — it's the name used everywhere: the HTTP
endpoint (`/mcp/<name>`, see below), the token scope (`kb:<name>:r|rw`), the clone directory (`<data>/<name>`), and the two
conventions below. With an `http(s)://` remote, if the file
`<git.token_dir>/<name>.token` exists (content: the token, trimmed), the server uses it as the HTTPS
credential for clone/fetch/pull/push of that KB, injected via `credential.helper` in the
per-process environment (`GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0`, D53): the token
never appears in argv, in the remote URL, or in `.git/config` — only the file's *path* is
embedded in the helper, which reads it (`cat`) whenever git invokes it. The existing SSH support
(`ssh_key`/`known_hosts`, global and per-KB, above) remains unchanged for `ssh://` remotes.
Similarly, `sops.age_key_dir` fixes a directory with a per-KB age key
(`<age_key_dir>/<name>.age`); the cascade for the KB's SOPS key resolution is: `kbs[].sops_age_key_file`
(explicit override) → `<sops.age_key_dir>/<name>.age` if the file exists → global
`sops.age_key_file`.

### MCP tool-name prefix (D102)

Every KB mounted on a multi-KB server exposes the *same* 20 tool names (`concept_read`, `search`,
...) on its own endpoint (`?kb=<name>` / `/mcp/<name>`). Clients that namespace MCP tools per
server — Claude Code, Codex, OpenCode — are unaffected by the collision. Kiro's CLI has a single,
flat tool namespace across every configured MCP server: mounting two KBs there means only one of
them keeps its tools reachable, silently.

The fix is an **opt-in, default-off** per-KB tool-name prefix: `kbs[].tool_prefix` (or the global
`mcp.tool_prefix_mode: kb-name`/`CARTOGRAPHER_MCP_TOOL_PREFIX_MODE=kb-name`, which derives the
prefix from the KB's own name for every KB that doesn't set its own `tool_prefix`) registers that
KB's tools as `<prefix>__<tool>` instead of `<tool>`. Precedence: `kbs[].tool_prefix` (explicit) >
`mcp.tool_prefix_mode`/env (global default) > off. It is off by default so that Claude
Code/Codex/OpenCode deployments, unaffected by the problem, never see a tool rename.

The raw prefix is sanitized before use: lowercased, every run of characters outside `[a-z0-9_]`
collapsed to a single `_`, leading/trailing `_` stripped. The server fails fast at startup (not at
first tool call) if, after sanitisation, the result is empty, starts with a digit, or the resulting
`<prefix>__<tool>` name exceeds 48 characters for any tool the KB registers — the error names the
KB and the offending tool/prefix. Read/write classification (§Auth) and the `agent`/`full` tools
profile (D65) both match on the tool name *after* stripping the prefix, so scoped tokens and the
tools profile behave identically with or without a prefix. When 2+ KBs are mounted, `serverInfo.name`
also becomes `cartographer:<kb>` (a single-KB deployment keeps the bare `cartographer` it has
always reported).

The managed instructions block Cartographer materializes into the client's memory file is
generated with each KB's **effective** tool names (D144): with a prefix configured it tells the
agent to call `<prefix>__search`, `<prefix>__concept_read`, … Turning a prefix on or off is
therefore a content change for that KB's instructions artifact, re-materialized on the next
`cartographer sync` — once, since the prefix is a stable config value.

The server prints one warning line on stderr at startup when it mounts 2+ KBs of which 2+ resolved
to no prefix, naming them (D144): those KBs register identical tool names and a flat-namespace
client keeps only one. It is a warning only — never a startup failure and never an implicit
prefix, so a Claude Code/Codex/OpenCode deployment is untouched.

`cartographer connect`/`cartographer sync` print a warning on stderr whenever the provider being
configured is `kiro` and 2+ MCP entries (i.e. 2+ KBs) are about to be written, regardless of
whether the server actually has prefixes configured (the client does not plumb `/health`'s
`tool_prefix` through that path): the operator adds `tool_prefix`/`tool_prefix_mode` to the server
config as the fix.

`GET /health`'s `kbs[]` items carry the KB's *effective* prefix as `tool_prefix` (omitted when
unprefixed) — the exact sanitised value the server registered its tools under, whether it came
from an explicit `kbs[].tool_prefix` or a derived `kb-name` mode (D120). Client-owned direct
administrative tool calls (`cartographer sync`'s `sync_pull`, `cartographer reindex`) discover
this value from a live `/health` snapshot before qualifying the tool name; they never re-derive
it from the server's own YAML. A stale client selection (a configured KB no longer among the
KBs the server currently advertises) is reported as an explicit error rather than guessed.

### Environment variables

Every startup option has a corresponding environment variable (the CLI flag takes precedence):

| Variable | Equivalent flag | Description |
|---|---|---|
| `CARTOGRAPHER_CONFIG` | `--config` | Path to a YAML config file (see §Configuration: flags, env, YAML) |
| `CARTOGRAPHER_KB` | `--kb` | Path(s) to the KB(s), comma-separated |
| `CARTOGRAPHER_KB_REMOTES` | — | Git remotes to clone into `--data` at startup, comma-separated (see §Bootstrapping a KB) |
| `CARTOGRAPHER_DATA` | `--data` | Directory whose direct subfolders are separate KBs (auto-discovery) |
| `CARTOGRAPHER_HTTP` | `--http` | HTTP listen address, e.g. `:39273` |
| `CARTOGRAPHER_TOKENS` | `--tokens` | Bearer tokens, comma/whitespace-separated. Each entry is either a bare `token` (admin, full access to every KB) or `token\|scope1;scope2` with per-KB scopes `kb:<name>:r\|rw` (scopes separated by `;`, never by spaces/commas, to avoid colliding with the between-entry separator). E.g. `admintok, readtok\|kb:wiki:r, writetok\|kb:wiki:rw;kb:notes:r` (D44). |
| `CARTOGRAPHER_AUTH` | — | Explicit auth toggle (see §Auth) |
| — | — | Fine-grained roles have no env form: `auth.roles` is YAML-only, because a rule is a structured object (see §Fine-grained roles). |
| `CARTOGRAPHER_GIT_AUTOCOMMIT` | `--git-autocommit` | Enables the git commit after every write. Default `true`; set to `false` or `0` to disable. |
| `CARTOGRAPHER_GIT_SYNC` | `--git-sync` | If the KB has an `origin` remote, runs fetch+pull-rebase before reads and writes, then pushes after writes (git as inter-instance sync). Default `true`; `false`/`0` to disable. Inert if the KB has no remote. Read-side sync is best-effort: a network failure serves the local replica; a rebase conflict is registered and the read also serves locally. |
| `CARTOGRAPHER_GIT_TOKEN_DIR` | — | Directory with one file per KB (`<dir>/<name>.token`) used as the HTTPS credential for that KB's git (D53, see §Bootstrapping a KB from a git remote). |
| `CARTOGRAPHER_SYNC_IN_WINDOW` | — | Go duration (e.g. `30s`, `0`) of the freshness window on `SyncIn`: within this window since the last successful fetch+pull, subsequent reads and writes skip it. Default `30s`; `0` = sync on every read and write (D76, D93). |
| `CARTOGRAPHER_SYNC_OUT_DEBOUNCE` | — | Go duration (e.g. `3s`, `0`) of the per-KB async push debounce: N writes in quick succession = 1 push, performed this long after the last signal. Default `3s`; `0` = push synchronously inline, no worker (rollback flag, D76). |
| `CARTOGRAPHER_SOPS_AGE_KEY_DIR` | — | Directory with a per-KB age key (`<dir>/<name>.age`), a fallback checked before `CARTOGRAPHER_SOPS_AGE_KEY_FILE` (D53). |
| `CARTOGRAPHER_TOOLS_PROFILE` | `--tools-profile` | `tools/list` tool profile: `agent` (default, only the core set for the LLM agent) \| `full` (all of them). Hidden tools remain callable via `tools/call` (D65, → `control-plane.md` §MCP API). |
| `CARTOGRAPHER_AUDIT_LOG` | — | Path to the audit log's JSONL file (e.g. `/data/audit.log`). If empty, audit is disabled. |
| `CARTOGRAPHER_AUDIT_KEY` | — | Ed25519 seed (hex, 64 chars) for signing entries. Requires `CARTOGRAPHER_AUDIT_LOG`. |
| `CARTOGRAPHER_SERVER_URL` | — | **Client** (not server): default server URL for `cartographer connect` on the client machine when no `.cartographer.yaml` exists yet. Precedence: existing yaml > env > `http://localhost:39273/mcp` (D64, `internal/clientconfig.Default`). |
| `CARTOGRAPHER_MCP_TOOL_PREFIX_MODE` | — | Global default for `mcp.tool_prefix_mode`: `off` (default) \| `kb-name`. Overridden per KB by `kbs[].tool_prefix` (D102, see §MCP tool-name prefix). |
| `CARTOGRAPHER_MCP_ALLOWED_ORIGINS` | — | Comma-separated browser origins allowed to reach `/mcp`, scheme and port included. Empty (default) accepts only an `Origin` matching the request's own `Host`; `*` accepts any; a request without an `Origin` header is unaffected (D128, → `transport-auth.md` §Origin). |

**`CARTOGRAPHER_AUTH`** — three modes:

| Value | Behavior |
|---|---|
| `false` / `0` / `no` / `off` | Auth disabled (e.g. a local service on loopback) |
| `true` / `1` / `yes` / `on` | Auth mandatory — fatal error at startup if no token is configured |
| unset | Auto: enabled if tokens are present, disabled otherwise |

#### Example: native local service (launchd/systemd, auth off, single client)

The local mode (D73) uses the binary already installed by `install.sh` as a **user service**:

```bash
cartographer service install                 # generates config + plist/unit, starts the server
cartographer service status                  # binary, config, installed/running/healthy
cartographer service start|stop|restart
cartographer service uninstall               # removes the service; config and data remain
cartographer kb create <name> --remote <url> # scaffolds a KB in the data dir, pushes it to <url> (D85, D134)
cartographer kb clone <remote>               # mounts an existing remote KB in that data dir (D97)
```

`service install` (idempotent: re-running it rewrites the plist/unit and restarts):
- generates `~/.config/cartographer/server.yaml` **only if it doesn't exist** (`--config` for a different path; `--data`, default `~/cartographer-data`, and `--http`, default `127.0.0.1:39273`, are only used at generation time — if the config already exists they are ignored with a warning: edit the file and run `service restart`);
- creates the configured data dir if it doesn't exist yet (D83), so a fresh install never leaves `serve` pointed at a missing directory;
- macOS: LaunchAgent `~/Library/LaunchAgents/com.cartographer.serve.plist` (`KeepAlive`, logging to `~/Library/Logs/cartographer/server.log`). The plist's binary path prefers a stable Homebrew symlink (`/opt/homebrew/bin/cartographer` or `/usr/local/bin/cartographer`) over the versioned Caskroom path, so it survives `brew upgrade` without a re-install (D83);
- Linux: systemd user unit `~/.config/systemd/user/cartographer.service` (log via `journalctl --user -u cartographer`; on a headless host, `loginctl enable-linger <user>` is needed for the service to survive logout).

Binds to **loopback** by default (`127.0.0.1:39273`) → auth stays in auto-off mode without exposing anything on the network. With an empty (or missing — D83: `serve` creates it and treats it as empty rather than failing) data dir the server starts with 0 KBs — `/health` is still up (liveness: `status:"ok"`) but `/ready` reports `503 {"ready":false}` (D84, §Observability): `cartographer kb create <name> --remote <url>` scaffolds a subfolder KB the same way `serve --kb <path> --init` would (D85) and pushes it to the empty repository at `<url>`, which becomes its `origin` — the remote is mandatory, `--no-remote` being the explicit opt-out for a local-only, non-durable KB (D134) — while `cartographer kb clone <remote>` mounts an existing OKF remote there (D97; `service install` itself prints a hint pointing at creation if it starts with 0 KBs mounted), or add `kbs:` entries to clone remotes (§Bootstrapping a KB) — either way, `service restart` (or `kb create --restart` / `kb clone --restart`, which do this for you and wait for the server to report healthy again) is what makes the new KB visible.

`service status` uses systemctl-like exit codes: `0` running, `3` installed but stopped, `4` not installed — this is what lets `install.sh update` automatically restart only a running service (see §Client installation).

**Data/code separation**: the cartographer repo contains no data. KBs live in the data dir (default `~/cartographer-data`); every subdirectory of it is a separate KB. For multiple users on shared KBs, see the k8s topology below — the server configuration is identical, only where it runs changes; for multiple instances on the same KB, see §Topologies (Multi-server).

Client-side convenience: if `cartographer connect` targets a loopback URL and the probe fails because the service isn't running, it directly offers to install+start the service (in the interactive flow) or suggests `cartographer service install` (in the non-interactive one).

#### Example: K8s pod (untrusted network, auth on, multi-client)

No init container: bootstrapping the git remotes (§Bootstrapping a KB from a git remote) is done by
the `serve` process itself at startup, reading `kbs:` from the mounted ConfigMap.

`ConfigMap` (mounts `config.yaml` at `/etc/cartographer`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cartographer-config
data:
  config.yaml: |
    http: ":39273"
    auth:
      mode: "on"
    data: /data
    kbs:
      - remote: ssh://git@gitea.example.com:2222/user/wiki-kb.git
    git:
      autocommit: true
      sync: true
      ssh_key: /etc/kb-ssh/id_ed25519
      known_hosts: /etc/kb-ssh/known_hosts
```

`Deployment` (excerpt — token and SSH key from a Secret, never in the ConfigMap):

```yaml
containers:
  - name: cartographer
    image: ghcr.io/beppetemp/cartographer:<tag>
    args: ["serve", "--config=/etc/cartographer/config.yaml"]
    env:
      - name: CARTOGRAPHER_TOKENS
        valueFrom:
          secretKeyRef: { name: cartographer-tokens, key: tokens }
    volumeMounts:
      - { name: config, mountPath: /etc/cartographer, readOnly: true }
      - { name: kb-ssh, mountPath: /etc/kb-ssh, readOnly: true }
      - { name: data, mountPath: /data }
volumes:
  - name: config
    configMap: { name: cartographer-config }
  - name: kb-ssh
    secret: { secretName: cartographer-kb-ssh, defaultMode: 0440 } # root-owned, private key + known_hosts
  - name: data
    persistentVolumeClaim: { claimName: cartographer-data }
```

This topology serves **multiple clients** (one or more agents connected via `cartographer connect` /
`cartographer sync`, all over HTTP): git remains the sync/backup layer between the server and the
remote KBs, not a requirement for the client.

### HTTP routing (D84)

`MultiKBServer.Handler` (`internal/mcpserver/httpserver.go`) selects a KB three ways:
- bare `/mcp` — auto-routes when exactly one KB is mounted (single-KB convenience);
- `/mcp?kb=<name>` — explicit selection by query parameter;
- `/mcp/<name>` — explicit selection by path (the form implied by `kbs[].name`, §Configuration above).

If both `/mcp/<name>` and `?kb=` are present and disagree, the request is rejected with `400
conflicting kb selection` rather than silently picking one; an unknown `<name>` (either form) is
`404 unknown kb`.

For a client connected to a multi-KB server, `cartographer connect` and `cartographer sync` use
the query form deliberately: they create one provider MCP entry per mounted KB,
`<server_name>-<kb> → /mcp?kb=<kb>`. This makes the selected KB explicit to every current client
without requiring a client to understand path routing. A one-KB server remains a single bare
`<server_name> → /mcp` entry for backwards compatibility.

### Runtime secrets

Git credentials, MCP tokens, and the age/SOPS key (`SOPS_AGE_KEY_FILE`) are
runtime secrets — never in the bundles. The same applies to the bootstrap SSH
key (`git.ssh_key`): mount or configure it outside committed YAML.

### Cold start

`clone`/`pull` of the KBs (including bootstrapping the remotes in `kbs:`, §Bootstrapping a KB from a git remote) + rebuilding missing indices. **Per-KB incremental** startup (a KB is served as soon as it's ready); with many KBs, the first startup is not instant.

The persisted SQLite index (`<kb>/.cartographer/index.db`) is excluded from git via `.git/info/exclude` (D62, never versioned): after a fresh clone (e.g. pod restart) it starts empty even if the concepts are already on disk. At startup, for every mounted KB, the server reconciles content hashes from the `.md` files against the persisted index, adding new concepts, refreshing changed ones, and removing vanished ones — keyword/FTS5 only (no embeddings: Ollama may be unreachable at boot). The same reconciliation runs after a `SyncIn` pull that moves HEAD. Best-effort: an error doesn't block startup, it is logged to stderr.

Imports and manual filesystem edits no longer require a service restart: call the MCP `reindex` tool, or run `cartographer reindex [--kb <name>]`. The CLI calls a healthy configured server over HTTP so it never opens that server's SQLite database; only when the server is down does it reconcile the configured local KB databases directly.

### Administration

Run `cartographer help` for the authoritative command list. Server lifecycle,
KB create/clone, client connection/sync, import, resolve and reindex have CLI
commands; concept writes, snapshots and contradiction resolution remain MCP
tool operations. There is no separate runtime token-registration command.

### Fine-grained roles

`auth.roles` declares named permission sets that narrow a token below whole-KB
granularity (D118). Semantics, the resource classes and the enforcement
guarantees are in [transport-auth](transport-auth.md) §Roles and fine-grained
permissions; this is the operator view.

```yaml
auth:
  mode: "on"
  roles:
    - name: runbook-editor
      rules:
        - kb: homelab
          access: rw            # r | rw
          maps: [infra]         # empty = every map
          types: [Runbook]      # empty = every type
        - kb: reference
          access: r
  tokens:
    - token: ${CARTOGRAPHER_TOKEN}
      id: ci                    # stable principal id for logs; derived from a
                                # token digest when omitted
      roles: [runbook-editor]
```

Rollout notes:

- **Nothing changes until you opt in.** Tokens without `roles` behave exactly as
  before: unscoped tokens stay admin, `scopes` keep granting whole-KB access.
- **Migrate one token at a time.** `roles` and `scopes` on the same token are
  unioned, so a token can gain a narrow role while keeping an existing scope.
- **Validation is fatal at startup.** A duplicate role or principal ID, an
  unknown role reference, an empty `kb`, an `access` other than `r`/`rw`, an
  empty or traversal selector, or a selector declared as both map and journal
  aborts the boot. Diagnostics name the role and rule index, never a token.
- **Selectors are not validated against KB content**: a role may reference a map
  that does not exist yet, and grants nothing until it does.
- Verify a rollout with a real request rather than by reading config: a token
  outside its perimeter receives a generic `not found`, deliberately
  indistinguishable from a missing concept.

### Audit log

`audit.log` turns on the compliance trail (D119). Semantics and the guarantees
are in [transport-auth](transport-auth.md) §Operational audit; this is the
operator view.

```yaml
audit:
  log: /var/lib/cartographer/audit.jsonl
  key_seed: ${CARTOGRAPHER_AUDIT_SEED}   # Ed25519 hex seed; enables signatures
  mode: required                         # best_effort (default) | required
  max_segment_bytes: 67108864
  archive_dir: /var/lib/cartographer/audit-archive
  retention_days: 400
```

Rollout notes:

- **Start in `best_effort`.** It is the default and the pre-D119 behaviour: a
  broken sink never blocks a call. Move to `required` only once the log's
  storage is as available as the service itself — in `required` mode an
  unwritable audit log takes MCP calls down with it, which is the correct
  trade for a compliance deployment and the wrong one everywhere else.
- **Back up `archive_dir` together with the checkpoint index.** Retention only
  ever deletes a segment already covered by a signed checkpoint, so verification
  still succeeds afterwards, but only if the index survives.
- **Keep the public key.** Without it `audit verify` checks the chain but not
  the signatures; it reports the unsigned count rather than silently passing.
- Verify from the operator machine, not the server: both commands read files
  and never contact a running instance, so they work during an outage.

## Observability

Cartographer emits structured operational messages and per-phase sync timing
to stderr. It does not expose Prometheus metrics or derive token/queue/search
SLIs from the audit file; the audit log is evidence for compliance review
(`cartographer audit verify|export`), not a metrics source.

**Liveness vs readiness (D84)**: `/health` is liveness — `status:"ok"` unconditionally (a probe that
restarted the process on `status != "ok"` must never fire from a KB-mounting issue); it also carries
a `ready: <bool>` field (single-KB server: always `true`; MultiKB: `len(kbs) > 0`) for callers that
want both signals from one request. `/ready` is the dedicated readiness endpoint: `200
{"ready":true}` once at least one KB is mounted, `503 {"ready":false,"kbs":0}` otherwise (e.g. a
fresh local install with an empty data dir, §Cold start). Point k8s
`livenessProbe` at `/health` and `readinessProbe` at `/ready`.

**Connected clients (D129)**: `GET /clients` reports which clients have talked to this process,
with their self-reported name and version, the protocol version and era, a request count and a
last-seen timestamp (shape and caveats: `transport-auth.md` §Client roster). Unlike `/health` and
`/ready` it requires a bearer token when auth is enabled, so it is not a probe target:

```bash
curl -sH "Authorization: Bearer $TOKEN" https://<host>/clients | jq
```

The tally lives in memory and resets when the pod restarts — it answers "what is connecting right
now", not "what connected last week"; that question belongs to the audit log.

## Backup and disaster recovery

### RPO/RTO

The git remote is the primary copy: the push **debounce** determines how many commits are lost if the host dies → fix the cadence and **replicate** the remote.

### Lost SOPS key

A truly unrecoverable scenario. Prevent it with multi-recipient escrow + restore testing (→ [`skills-services-secrets.md`](skills-services-secrets.md) §break-glass).

### Working-tree crash recovery

The server does not repair arbitrary interrupted git state at boot. Before
mounting, inspect and clean up an active merge/rebase, stale lock file or
unexpected working-tree changes using normal git recovery procedures. Keep a
backup before discarding any state.

### External component failures

| Component | Behavior |
|---|---|
| Git remote unreachable | The local commit remains; asynchronous push logs the failure and a later sync retries |
| Invalid/expired git credentials | The git command fails and the error is surfaced; rotate the configured token/key |
| Disk full | Atomic writes fail cleanly |
| Corrupted SQLite index | Rebuilt from the `.md` files |

## Upgrades, schema migration, and repo growth

- **Server upgrade** with mounted KBs: graceful HTTP shutdown flushes pending
  pushes before the process exits.
- **Native local upgrade** (Homebrew Cask or `install.sh update`): self-repairing since D121. `brew upgrade` replaces the binary behind Homebrew's stable symlink, but cannot replace the image of a process already running it, so both update paths call `cartographer upgrade-repair` on the newly linked binary. It gracefully replaces a **running** service (`SIGTERM`, so in-flight requests drain and pending pushes flush), waits until `/health` reports `status:"ok"` **and** the installed version, then reconciles the configured providers in place with the ordinary `cartographer sync` policy. It is idempotent: if `/health` already advertises the installed version it skips the restart and only syncs. A deliberately stopped or uninstalled service stays that way, and a client pointed at a non-loopback (or different local) endpoint has its provider sync skipped rather than run against a possibly remote server. **Still needed afterwards:** restart already-open provider sessions, which is what reopens the MCP connection and reloads the repaired configuration.
- **When the repair does not complete**: exit `1` means the binary and the service are fine but provider sync is pending — the underlying error is printed and `cartographer sync` is the retry. Exit `2` means a running native service could not be replaced and verified; no provider sync was attempted, and `install.sh update` fails visibly while a Cask upgrade still completes (the transaction is not rolled back). Investigate with `cartographer service status` and the server log, then retry `cartographer upgrade-repair`. `cartographer status` reports any residual skew and points at the same command.
- **Kubernetes and remote servers**: unchanged. Bump the image tag; connected clients surface the version skew (D95) while the rollout catches up. `upgrade-repair` is for native local installations only.
- **KB format changes**: follow the release notes and back up the git remote
  before a migration. There is no general automatic schema migration engine.
- **Repo growth**: keep large binary/source archives outside the KB and link
  them; Cartographer is optimized for Markdown knowledge repositories.

## CI/CD: release + deploy pipeline

Three GitHub Actions workflows (`.github/workflows/`):

- **`ci.yml`** — on every PR and push to `main`: the `test` job (`make vet && make test`, Go from `go.mod`, git identity configured for git-backed tests) and the `pr-title` job (lints the PR title as a conventional commit: with squash-merge the title becomes the commit on `main` that release-please reads). `test` is the required status check on `main`'s ruleset.
- **`release-please.yml`** — on every push to `main`: maintains the **release PR** (semver bump computed from conventional commits + `CHANGELOG.md`). On merge, it creates the `vX.Y.Z` tag and GitHub Release with the notes. Uses the `RELEASE_PLEASE_TOKEN` PAT (PRs opened with the default `GITHUB_TOKEN` do not trigger `pull_request` workflows).
- **`release.yml`** — on the `v*` tag: **GoReleaser** (`.goreleaser.yaml`) builds the `darwin/linux × amd64/arm64` binaries (`-X main.version=v<semver>`, read by `cartographer version`), attaches them to the existing release with `sha256sums.txt` (`release.mode: keep-existing` — the release and its notes belong to release-please) and publishes the Homebrew cask on `BeppeTemp/homebrew-tap` (`HOMEBREW_TAP_TOKEN` PAT, macOS only); in parallel, buildx publishes the multi-arch image `ghcr.io/beppetemp/cartographer:<tag>` + `:latest`. After the image push, the `mcp-registry` job publishes the server's metadata to the official **MCP Registry** (`io.github.beppetemp/cartographer`, OCI package pointing at the ghcr image): `mcp-publisher login github-oidc` (no long-lived secret) + `publish` with `server.json` (repo root; `version` and image tag are stamped by the job). Ownership is verified via the `io.modelcontextprotocol.server.name` LABEL baked into the image.

Dependency updates (Go modules and action versions) come from **Dependabot** (`.github/dependabot.yml`, weekly, grouped PRs).

**Deployment** to the maintainer's infrastructure happens outside the pipeline: the cluster is GitOps, and the image tag bump in the manifests repo is reconciled by Flux (D68). CI does not run `kubectl apply`: it would be redundant, would conflict with Flux's drift control, and would require a long-lived kubeconfig in the repo's secrets.

Secrets required in the GitHub repo: `RELEASE_PLEASE_TOKEN`, `HOMEBREW_TAP_TOKEN`. `GITHUB_TOKEN` is provided automatically by Actions (for ghcr.io the `packages: write` permission declared in the workflow is enough).

The end-to-end operator procedure (release, rollout verification, local client update) lives outside the repo, in the maintainer's local tooling: it contains details of the private deployment infrastructure.

### Client installation (`install.sh`)

Preferred path on macOS: `brew install beppetemp/tap/cartographer` (cask from the tap). Alternatively (and on Linux), `install.sh` (repo root) downloads the latest `cartographer` binary from the most recent GitHub Release for the current platform:

```bash
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
curl -fsSL .../install.sh | sh -s -- update      # updates if not already on the latest release
curl -fsSL .../install.sh | sh -s -- uninstall
```

No package-manager dependency: pure POSIX `sh`, verifies the checksum via `sha256sums.txt` if present in the release, installs into `/usr/local/bin` (falling back to `~/.local/bin` if not writable, overridable with `CARTOGRAPHER_INSTALL_DIR`). See `docs/configurator.md` §Installation.

After replacing the binary the script runs `cartographer upgrade-repair` (D121, §Upgrades, schema migration, and repo growth) instead of a bare `service restart`: a running service is gracefully replaced and version-verified, a stopped or absent one is left alone, and the configured providers are re-synchronized. A pending provider sync (exit `1`) is logged without failing the update; an unverifiable running service (exit `2`) fails the `update` command **after** the new binary is in place. The generated Homebrew Cask runs the same command from its post-install hook, non-fatally: its source of truth is `.goreleaser.yaml` in this repository, never a hand edit in the tap.

After an `update`, if the local service (§Example: native local service) is found **running** (`service status` → exit 0) the script restarts it automatically, so the daemonized server switches to the new binary right away; a service stopped on purpose (exit 3) stays stopped.

> The repo is public on GitHub: a plain `curl` works with no authentication. The script supports an optional `GITHUB_TOKEN` to avoid API rate limits on frequent runs (e.g. CI).
