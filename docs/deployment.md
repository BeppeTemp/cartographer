# Deployment, operations, and disaster recovery

## Deployment and packaging

The server is distributed as a native **Go binary** (via `install.sh`, see §Client installation — client and server are the same binary). The Docker image only exists as a CI artifact for k8s deployment: **it is not a local deployment mode** (D73 — containers on macOS mean a slow virtualized filesystem for git/SQLite, and an always-on VM).

### Topologies

| Topology | Description |
|---|---|
| **Local** | The binary runs as a native user service (`cartographer service`, launchd/systemd); the client on the same machine points to `127.0.0.1`. |
| **Shared server** | HTTP+OAuth/token to multiple agents, on a trusted network / reverse proxy (k8s). |
| **Multi-server** | Several instances (local and/or shared) mount the **same KB** from the same git remote: every write does pull-rebase → commit → push, git is the sync fabric. Concurrent writes to the same concept → rebase-conflict and `needs-resolution` (expected behavior, not a failure). A client points to **one server at a time**. |

### State and volumes

The server process is **ephemeral** (k8s pod or local service); what persists on volume/disk:
- The KBs' git working trees.
- SQLite indices (optional, rebuildable).
- The **audit log** (not in the `.md` git repo → persistent storage, must not be lost with the ephemeral container).

### Configuration: flags, env, YAML

`cartographer serve` resolves its configuration from three sources, in order of precedence (highest first):
**CLI flag > environment variable > YAML file (`--config`/`CARTOGRAPHER_CONFIG`) > default**
(`internal/config`, `config.Default()`). The YAML file is optional: without `--config`, only
defaults + env + flags apply, as in earlier versions.

Full annotated example: [`config.example.yaml`](../config.example.yaml) (repo root).
Schema (`internal/config.Config`, YAML tags in parentheses):

```yaml
http: ":8080"                 # (http) listen address; absent = stdio
init: true                    # (init) initialize missing KBs
auth:
  mode: "on"                  # (auth.mode) on | off | auto (default auto: on if tokens are present)
  tokens: [...]                # (auth.tokens) prefer env CARTOGRAPHER_TOKENS instead (secret)
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
git:
  autocommit: true            # (git.autocommit) commit after every write
  sync: true                  # (git.sync) fetch/pull-rebase+push if the KB has a remote
  ssh_key: /etc/kb-ssh/id_ed25519      # (git.ssh_key) default SSH identity for cloning kbs[] remotes
  known_hosts: /etc/kb-ssh/known_hosts # (git.known_hosts) host verification for the same clone
  author_name: cartographer            # (git.author_name) default author/committer (final fallback)
  author_email: cartographer@localhost # (git.author_email)
  token_dir: /etc/kb-git-tokens         # (git.token_dir) directory <token_dir>/<name>.token (D53, see below)
  in_window: 30s                # (git.in_window) SyncIn freshness window: within this duration
                                # since the last successful fetch+pull, subsequent writes skip it
                                # (no-op). Default 30s, 0 = sync on every write (D76)
  out_debounce: 3s              # (git.out_debounce) debounce of the per-KB async push after the
                                # commit: N writes in quick succession = 1 push. Default 3s, 0 = push
                                # synchronously inline, no worker (rollback flag, D76)
search:
  ollama_url: ""               # (search.ollama_url) enables semantic search
  ollama_model: nomic-embed-text
audit:
  log: ""                      # (audit.log) path to the audit log's JSONL file
  key_seed: ""                 # (audit.key_seed) Ed25519 hex seed for signing
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
(`CommitOp`, conflict resolution). Fallback cascade: the KB's value → global `git.*` → hard-coded
default (`cartographer`/`cartographer@localhost`); the default committer is the default author
(the KB's own or the global one) if `committer_*` is not set. The per-KB environment assembled
this way **overrides** the server's process environment (the reverse of the "environment wins"
rule that applies to the global `git.ssh_key`/`GIT_SSH_COMMAND` above) — see
`internal/gitx.runGitEnv`. With the identity set via `kbs[].author_name`/`author_email`/`committer_*`
(or via global `git.author_name`/`author_email`), any `GIT_AUTHOR_*`/`GIT_COMMITTER_*` in the k8s
Deployment's `env:` become redundant and can be removed.

**Explicit KB name, git token, and per-KB SOPS key by convention (D53)**: `kbs[].name` fixes the
KB's name (overrides the name derived from remote/path) — it's the name used everywhere: the HTTP
endpoint (`/mcp/<name>`), the token scope (`kb:<name>:r|rw`), the clone directory (`<data>/<name>`), and the two
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

### Environment variables

Every startup option has a corresponding environment variable (the CLI flag takes precedence):

| Variable | Equivalent flag | Description |
|---|---|---|
| `CARTOGRAPHER_CONFIG` | `--config` | Path to a YAML config file (see §Configuration: flags, env, YAML) |
| `CARTOGRAPHER_KB` | `--kb` | Path(s) to the KB(s), comma-separated |
| `CARTOGRAPHER_KB_REMOTES` | — | Git remotes to clone into `--data` at startup, comma-separated (see §Bootstrapping a KB) |
| `CARTOGRAPHER_DATA` | `--data` | Directory whose direct subfolders are separate KBs (auto-discovery) |
| `CARTOGRAPHER_HTTP` | `--http` | HTTP listen address, e.g. `:8080` |
| `CARTOGRAPHER_TOKENS` | `--tokens` | Bearer tokens, comma/whitespace-separated. Each entry is either a bare `token` (admin, full access to every KB) or `token\|scope1;scope2` with per-KB scopes `kb:<name>:r\|rw` (scopes separated by `;`, never by spaces/commas, to avoid colliding with the between-entry separator). E.g. `admintok, readtok\|kb:wiki:r, writetok\|kb:wiki:rw;kb:notes:r` (D44). |
| `CARTOGRAPHER_AUTH` | — | Explicit auth toggle (see §Auth) |
| `CARTOGRAPHER_GIT_AUTOCOMMIT` | `--git-autocommit` | Enables the git commit after every write. Default `true`; set to `false` or `0` to disable. |
| `CARTOGRAPHER_GIT_SYNC` | `--git-sync` | If the KB has an `origin` remote, runs fetch+pull-rebase before and push after every write (git as inter-instance sync). Default `true`; `false`/`0` to disable. Inert if the KB has no remote. |
| `CARTOGRAPHER_GIT_TOKEN_DIR` | — | Directory with one file per KB (`<dir>/<name>.token`) used as the HTTPS credential for that KB's git (D53, see §Bootstrapping a KB from a git remote). |
| `CARTOGRAPHER_SYNC_IN_WINDOW` | — | Go duration (e.g. `30s`, `0`) of the freshness window on `SyncIn`: within this window since the last successful fetch+pull, subsequent writes skip it. Default `30s`; `0` = sync on every write (D76). |
| `CARTOGRAPHER_SYNC_OUT_DEBOUNCE` | — | Go duration (e.g. `3s`, `0`) of the per-KB async push debounce: N writes in quick succession = 1 push, performed this long after the last signal. Default `3s`; `0` = push synchronously inline, no worker (rollback flag, D76). |
| `CARTOGRAPHER_SOPS_AGE_KEY_DIR` | — | Directory with a per-KB age key (`<dir>/<name>.age`), a fallback checked before `CARTOGRAPHER_SOPS_AGE_KEY_FILE` (D53). |
| `CARTOGRAPHER_TOOLS_PROFILE` | `--tools-profile` | `tools/list` tool profile: `agent` (default, only the core set for the LLM agent) \| `full` (all of them). Hidden tools remain callable via `tools/call` (D65, → `control-plane.md` §MCP API). |
| `CARTOGRAPHER_AUDIT_LOG` | — | Path to the audit log's JSONL file (e.g. `/data/audit.log`). If empty, audit is disabled. |
| `CARTOGRAPHER_AUDIT_KEY` | — | Ed25519 seed (hex, 64 chars) for signing entries. Requires `CARTOGRAPHER_AUDIT_LOG`. |
| `CARTOGRAPHER_SERVER_URL` | — | **Client** (not server): default server URL for `cartographer connect` on the client machine when no `.cartographer.yaml` exists yet. Precedence: existing yaml > env > `http://localhost:8080/mcp` (D64, `internal/clientconfig.Default`). |

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
```

`service install` (idempotent: re-running it rewrites the plist/unit and restarts):
- generates `~/.config/cartographer/server.yaml` **only if it doesn't exist** (`--config` for a different path; `--data`, default `~/cartographer-data`, and `--http`, default `127.0.0.1:8080`, are only used at generation time — if the config already exists they are ignored with a warning: edit the file and run `service restart`);
- macOS: LaunchAgent `~/Library/LaunchAgents/com.cartographer.serve.plist` (`KeepAlive`, logging to `~/Library/Logs/cartographer/server.log`);
- Linux: systemd user unit `~/.config/systemd/user/cartographer.service` (log via `journalctl --user -u cartographer`; on a headless host, `loginctl enable-linger <user>` is needed for the service to survive logout).

Binds to **loopback** by default (`127.0.0.1:8080`) → auth stays in auto-off mode without exposing anything on the network. With an empty data dir the server starts with 0 KBs (`/health` is still up): create a subfolder per KB (or add `kbs:` entries to clone remotes, §Bootstrapping a KB) and `service restart`.

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
    http: ":8080"
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

### Runtime secrets

Git credentials, MCP tokens, **and** the age/SOPS key (`SOPS_AGE_KEY_FILE`/`SOPS_AGE_KEY_CMD`, or an IAM role for KMS) are runtime secrets — **never** in the bundles. The same applies to the bootstrap SSH key (`git.ssh_key`): always from a mounted Secret/volume, never in the ConfigMap nor in the committed YAML file.

### Cold start

`clone`/`pull` of the KBs (including bootstrapping the remotes in `kbs:`, §Bootstrapping a KB from a git remote) + rebuilding missing indices. **Per-KB incremental** startup (a KB is served as soon as it's ready); with many KBs, the first startup is not instant.

The persisted SQLite index (`<kb>/.cartographer/index.db`) is excluded from git via `.git/info/exclude` (D62, never versioned): after a fresh clone (e.g. pod restart) it starts empty even if the concepts are already on disk. At startup, for every mounted KB, the server detects an empty index (`COUNT(*)` on the `concepts` table == 0) and rebuilds it automatically from the `.md` files — the same logic as `index_rebuild`, but keyword/FTS5 only (no embeddings: Ollama may be unreachable at boot). Best-effort: an error doesn't block startup, it's just logged to stderr. `mcpserver.EnsureSQLIndexFresh`, hooked into `cmd/cartographer/serve.go` after opening each `sqlindex.Index`.

### Administration

**Server CLI** (purely administrative): tokens, KB registration, index rebuild, snapshot, `conflict_resolve`. It does not write content through the agent's tools.

## Observability

Metrics derived from the audit log:
- KBs in `needs-resolution`.
- Ingest queue depth.
- Git push lag/failures.
- p99 latency of `search`.
- Tokens consumed per phase (ingest, judge, deep lint, embedding).

**`/health`** and readiness endpoints. SLI/SLO and alert thresholds. Without these the system is a black box in production.

## Backup and disaster recovery

### RPO/RTO

The git remote is the primary copy: the push **debounce** determines how many commits are lost if the host dies → fix the cadence and **replicate** the remote.

### Lost SOPS key

A truly unrecoverable scenario. Prevent it with multi-recipient escrow + restore testing (→ [`skills-services-secrets.md`](skills-services-secrets.md) §break-glass).

### Working-tree crash recovery

At boot the server detects and repairs an interrupted git state: a half-finished rebase → `abort`, leftover stash, an orphaned `index.lock`, a crash between rename and commit.

### External component failures

| Component | Behavior |
|---|---|
| Git remote unreachable | Distinguished from non-fast-forward; retried with backoff |
| OAuth AS down | JWKS cache + grace period |
| Expired git credentials | 401/403 ≠ network down; flagged explicitly |
| Disk full | Atomic writes fail cleanly |
| Corrupted SQLite index | Rebuilt from the `.md` files |

## Upgrades, schema migration, and repo growth

- **Server upgrade** with mounted KBs: drain (flush pushes, release mutexes) before restart.
- **Schema versioning**: bundle-level `okf_version` **and** per-concept `schema_version`, with a migration procedure for existing `.md` files (no destructive overwrite).
- **Repo growth**: retention/archival of closed journals, `git-LFS` or blobless/shallow clone for large `raw/` content, incremental index for `commit_gate`.

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

After an `update`, if the local service (§Example: native local service) is found **running** (`service status` → exit 0) the script restarts it automatically, so the daemonized server switches to the new binary right away; a service stopped on purpose (exit 3) stays stopped.

> The repo is public on GitHub: a plain `curl` works with no authentication. The script supports an optional `GITHUB_TOKEN` to avoid API rate limits on frequent runs (e.g. CI).
