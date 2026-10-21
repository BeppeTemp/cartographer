# Deployment, operations, and disaster recovery

## Deployment and packaging

The server is distributed as a native **Go binary** (via `install.sh`, see §Client installation — client and server are the same binary). The Docker image only exists as a CI artifact for k8s deployment: **it is not a local deployment mode** (D73 — containers on macOS mean a slow virtualized filesystem for git/SQLite, and an always-on VM).

### Topologies

| Topology | Description |
|---|---|
| **Local** | The binary runs as a native user service (`cartographer service`: launchd, systemd, or a Windows Scheduled Task); the client on the same machine points to `127.0.0.1`. |
| **Shared server** | HTTP with optional static bearer tokens for multiple agents, on a trusted network or behind a reverse proxy (for example Kubernetes). |

### Provisioning artifact signing

A KB signs its artifacts with `<kb>.signing` in the credentials directory
(§Credentials directory, D905): a 32-byte Ed25519 seed in hex. There is no
`default.signing`, because a signature names its KB, so a KB without the file
serves unsigned artifacts. Mount it from a secret manager or protected secret volume: never
commit it, expose it in health/MCP output, or place it in logs. The server logs
only the KB and derived key ID. Distribute and pin the corresponding public key
on each client before switching a signer; unsigned KBs remain supported through
the explicit client trust policy.

### KB-provided MCP descriptors

The descriptors under a KB's `mcp/*.json` are always distributable (D906): the server
lists every valid one in the manifest and in `artifact_list`, and `artifact_read` returns it.
There is no allow-list on the server (`mcp_allowlist`, in a KB's `settings.yaml` or `kbs[]`,
is a removed key: it is ignored with a warning and dropped when the file is realigned). The
consent lives on the client, which asks for a per-descriptor, hash-bound approval before it
installs one (`docs/sync.md` §MCP servers). Headers and environment references stay references
and are never written to the manifest as values.
| **Partitioned servers** | Several instances mount different KBs. A client can connect to entries from more than one server. Do not use several active writer servers as replicas for the same KB; git conflict recovery is a safety net, not a write-scaling protocol. |

### State and volumes

The server process is **ephemeral** (k8s pod or local service); what persists on volume/disk:
- The KBs' git working trees.
- SQLite indices (optional, rebuildable).
- Optional local runtime files under `.cartographer/`. Search indexes are
  rebuildable; conflict registries are operational state and should survive a
  restart until resolved; `search-misses.jsonl` (D247) is a bounded log of
  searches that found nothing, disposable, lost harmlessly with the volume.
  An entry ages out after 30 days; one whose search finds something now is
  reported `resolved` by `kb_status` before that (D319). Deleting the file is
  the only manual reset. `lint-history.jsonl` (D370) holds one sample a day of
  the whole-KB lint totals (`{at, total, by_handler}`, the latest
  of the UTC day, 90 days kept, pruned on write): derived state for
  `kb_status.lint_trend`, rewritten whole, never in the KB's git
  history, and lost harmlessly with the volume (the trend restarts from the
  next lint). `usage.json` (D326) holds the artifact usage clients
  report on `sync`: on an `emptyDir` it is lost at every restart, and until each
  client syncs again `kb_status.usage` is `no_data` (with `no_report_since`, the
  start time, D333) and no artifact reads as stale. Keep `.cartographer/` on
  the KB's persistent volume when that signal matters.

The audit log is always on (D905): MCP tool execution appends an attempt+completion event
pair per call to `<data>/audit/audit.log`, or to `audit.log` when it is set, so the file is a complete
operational request log (D119), signed with `audit.key` from the credentials directory. See
§Audit log below and [transport-auth](transport-auth.md) §Operational audit. `audit.retention_days` defaults to 90 (D325; `0` keeps everything). A configuration that `service install` wrote before D905 sets `audit.log: <data>/audit.log` and keeps that path.

### Configuration: flags, env, YAML

`cartographer serve` resolves its configuration from three sources, in order of precedence (highest first):
**CLI flag > environment variable > YAML file (`--config`/`CARTOGRAPHER_CONFIG`) > default**
(`internal/config`, `config.Default()`). The YAML file is optional: without `--config`, only
defaults + env + flags apply, as in earlier versions.

Which layer set each value is recorded as the layers are applied (`Config.Origins`, D743) and shown by
`cartographer config` and the Atlas Settings page: `default`, `file`, `env` or `flag`, per-KB keys by mounted name. A key
written in the file equal to its default is still `file`. Secrets show only `(set)` or `(unset)`.

Full annotated example: [`config.example.yaml`](https://github.com/BeppeTemp/cartographer/blob/main/config.example.yaml) (repo root).
The example is **generated** from the settings schema (`Schema` in `internal/config/schema.go`: every key with its
scope, type, values, default, description, the environment variable and flag that override it, and whether it needs a
restart) by `make settings-example`; a test fails when the committed file differs, and another when a key reaches
`rawConfig` or `KBSpec` without a schema entry. Edit the schema, not the example. A `config.yaml` written by
`service install` is self-documenting the same way: the keys it sets carry their comment and the unset ones are listed
commented out under `# --- available settings: uncomment a key to set it ---` (the divider older versions wrote,
`# --- available settings ---`, is read the same), a blank line between keys, each with the value to uncomment: the key's
`Example` when its schema entry has one (a map or a list is shown in block style, indented under the key), its default
otherwise. Comments are ignored by the parser, so a file without them behaves
identically. `kb create`/`kb clone` and `kb rename` give a `kbs[]` entry they add the comment of the keys they set,
and leave every comment already in the file alone.

The old names of renamed KBs are not a setting (D906): they are a list of `{from, to}` items in the **server state file** `<data>/.cartographer/state.json` (`kb_aliases`), beside `update-check.json`. `kb rename` and `kb unmount` write it (offline, atomically), the server reads it at its start, and an operator never needs to edit it. It makes a routed `?kbs=<from>` select the KB mounted as `<to>`; it is validated when read (valid KB names, no `from` equal to its `to`, no duplicate `from`), and a mounted name always wins over an alias. A server YAML that still carries a `kb_aliases` key is realigned (D903): its items are moved into the state file first, and until then they resolve as before. A server with no `data` directory has no state file, hence no aliases.

Three behaviours that used to be settings are fixed (D906): `serve` never creates a KB (a missing one fails with *create it with `cartographer kb create`*; the `init` key and `--init` flag are removed); `tools/list` shows the core set (below) with every other tool still callable by name (`tools.profile` is removed); and a request to `/mcp` with an `Origin` header is accepted only when the origin's host and port are the request's `Host`, one with no `Origin` header (a non-browser client) always is (`mcp.allowed_origins` is removed, see [transport-auth](transport-auth.md) §Origin). Each removed key, environment variable and flag is warned about and dropped by the realignment, never an error that stops the start.

A server that would listen **off-loopback with authentication off** refuses to start and says why: loopback is a listen host in `127.0.0.0/8`, `::1` or `localhost`; `auth.mode: off`, or `auto` with no token, is auth off; `:39273` and `0.0.0.0:39273` listen on every interface. The local service pins auth off on `127.0.0.1` (D268) and is unaffected.

One startup option is deliberately **flag-only**, with no YAML key and no environment variable:
`serve --log-file <path>` appends the server log to a file instead of writing it to stderr,
creating the parent directory and never rotating (neither does launchd). It exists because a
Windows Scheduled Task's action cannot declare a log destination the way a launchd plist does, and
Windows has no journald — so the process takes the path itself and the task's argv stays flat
enough for `EffectiveConfigPath` to read `--config` back out of it
([D217](decisions/D217-the-native-service-on-windows-is-a-per-user-scheduled-task.md)). It applies
on every platform; a path that cannot be opened is a startup error, because a service that silently
logs nowhere is the failure the flag exists to prevent. `cartographer sync --log-file` is the
client-side counterpart, used by the scheduled sync task. It is not in the YAML because it belongs
to *this invocation*, not to the served configuration: two services sharing one config file must
still be able to log to two files.

Schema (`internal/config.Config`, YAML tags in parentheses):
```yaml
http: ":39273"                # (http) listen address; absent = stdio. Off-loopback needs a token (D906)
auth:
  mode: "on"                  # (auth.mode) on | off | auto (default auto: on if tokens are present)
  tokens: [...]                # (auth.tokens) prefer env CARTOGRAPHER_TOKENS instead (secret)
  roles: [...]                 # (auth.roles) named fine-grained permission sets (D118), see below
data: /data                   # (data) directory with KB auto-discovery (one subfolder = one KB);
                              # paths already mounted explicitly (kbs[], including remote KBs cloned
                              # into this directory) are excluded from discovery — no double mount
credentials_dir: /etc/cartographer/credentials # (credentials_dir) every secret file, looked up by
                              # KB name (D905, §Credentials directory); default <data>/credentials

kbs:                          # (kbs[]) KBs to clone from a git remote (bootstrap, see below); a KB
                              # that is a folder under data needs no entry (D903)
  - remote: ssh://git@host:2222/user/wiki-kb.git
  - remote: https://gitea.example.com/team/shared-kb.git
    name: shared-kb                                  # (kbs[].name) explicit name (D53): overrides
                                                      # the name derived from the remote — the HTTP
                                                      # endpoint, token scope, clone dir, and the
                                                      # KB's files in credentials_dir all use
                                                      # this name
audit:
  log: ""                      # (audit.log) the always-on audit log; empty = <data>/audit/audit.log,
                               # signed with audit.key from credentials_dir (D905)
  mode: best_effort            # (audit.mode) best_effort (default) | required
  max_segment_bytes: 0         # (audit.max_segment_bytes) rotate past this size; 0 = package default
  archive_dir: ""              # (audit.archive_dir) rotated segments; empty = beside the log
  retention_days: 90           # (audit.retention_days) delete checkpointed segments older than N days; default 90; 0 = keep forever
```

`kbs[]` (YAML) is additive to `CARTOGRAPHER_KB_REMOTES` (append, not replace) — all listed KBs get
mounted — and every entry has a `remote`: a KB without one is a folder under `data`, which is discovered
(there is no `kbs[].path`). The `--kb` flag and `CARTOGRAPHER_KB` mount one more folder from the command
line, for development and tests: it is not a key of the YAML file, and a folder mounted that way has no
remote, so it never syncs. Scalar fields (`http`, `data`, tokens, ...) follow flag > env > YAML
precedence: the last explicitly set level wins.

**Git: one way to write (D903).** Cartographer has no git settings of its own: the SSH key and the token that reach a remote are files of the credentials directory (D905). Every write is one commit on the KB's branch, and that is not configurable; a KB syncs exactly
when it has a remote (fetch and pull-rebase before a read or write, push after a write) and a KB without one
never contacts anything. The freshness window of the pull (30s) and the debounce of the background push (3s)
are constants. There is no pull-request boundary and no forge: the server git profile (D117) and its keys
(`git.profile`, `git.base_branch`, `git.working_branch`, `git.forge`, `git.github_*` and their `kbs[]` forms)
are gone with the `pr_status` and `pr_finalize` tools, and so are `git.autocommit`, `git.sync`, `git.in_window`,
`git.out_debounce` and `kbs[].path`. A KB that has a remote and was kept from pushing with `git.sync: false`
starts pushing to it after the upgrade (the start only warns): remove the remote from the clone to keep it local.
Cartographer pushes to the KB's branch itself; if the remote refuses
because the branch is protected, the push is reported as that (`sync_status` `failed`, `last_error` naming the
way out: unprotect the branch, or set `branch` in the KB's `settings.yaml`), the change stays committed in the
clone, and the push is not retried.

**Upgrading: realignment (D903).** The first start of a version that removed keys rewrites the server YAML into
its commented form: the removed keys are dropped, values that switched a feature off are reset to the default,
every other value, comment and key order stays, and the original is kept beside it as
`config.yaml.bak-<version>` (`.1`, `.2`… when one of that name exists). The server logs what it changed, one line
per key, and does nothing, writing no backup, to a file already in that form; a file it cannot write (a
read-only mount) keeps working, since a removed key is ignored with a warning that names its replacement.
The same applies to a removed environment variable and flag (`CARTOGRAPHER_GIT_SYNC`, `--git-sync`, ...): ignored
with a warning, never an error. Each KB's `settings.yaml` is realigned the same way as one commit by the server,
through the normal commit and push, so it is revertible; a file with uncommitted edits is left alone. `write_gate`, `auto_repair`, `repair_on_write` and the `doctor_*` keys (D907), in a KB's `settings.yaml` or in a `kbs[]` entry, go the same way: the gate and the safe repairs are fixed behaviour. The client
file `~/.cartographer.yaml` is realigned by the first `cartographer sync`. Two removed settings are refused and
not ignored, because ignoring them would change where writes go. The server profile (`git.profile: server`,
`kbs[].git_profile: server`, `CARTOGRAPHER_GIT_PROFILE=server`, or `git_profile: server` in a KB's
`settings.yaml`) would write straight to the branch it protected: the server does not start (or, for a KB's own
file, that KB does not mount) and says what to do. A `kbs[].git_branch` still set would move the KB's writes to
the remote's default branch: the server does not start until that value is set as `branch` in the KB's
`settings.yaml` (committed on the default branch) and the key is removed. A refused file is not rewritten. The
registry of removed keys sits next to each schema
(`config.Retired`, `kb.SettingsRetired`, `clientconfig.Retired`).

### Credentials directory (D905)

Every secret file the server reads is in one directory, `credentials_dir` (env `CARTOGRAPHER_CREDENTIALS_DIR`,
default `<data>/credentials`), and is found by the KB's name (`kbs[].name`, or the name derived from the
remote). On Kubernetes it is one mounted Secret (§Example: K8s pod).

| File | What it holds | When it is missing |
|---|---|---|
| `<kb>.ssh`, else `default.ssh` | SSH private key for the KB's `ssh://` remote | git's own SSH setup |
| `known_hosts` | host keys of every SSH remote, in one file | ssh's own known hosts |
| `<kb>.token`, else `default.token` | HTTPS git token (§Bootstrapping a KB from a git remote) | no credential |
| `<kb>.age`, else `default.age` | age identity that decrypts the KB's SOPS secrets | secrets report a missing key |
| `<kb>.signing`, no default | artifact signing seed, 64 hex characters (§Provisioning artifact signing) | unsigned artifacts |
| `audit.key` | audit log signing seed, 64 hex characters | generated at the first start |

`default.token`, `default.ssh` and `default.age` are offered to every KB that has no file of its own,
whatever its remote host: a KB whose remote is on another host than the others should get its own
`<kb>.token` (or `.ssh`), or the default credential is sent to that host.

The lookup applies to every mounted KB, discovered or configured. A file counts when it is a regular file
or a symlink to one, which is how a Kubernetes Secret mounts its keys; a missing file means the credential is
not configured. A KB name that would point outside the directory has no file of its own.

- **Read-only is fine.** The server writes into the directory only to generate `audit.key` and to place the
  secrets of removed settings when upgrading (below), `0600` in a `0700` directory, and never replaces a
  file. A read-only directory needs `audit.key` in it (`openssl rand -hex 32`): without it, with
  `audit.mode: best_effort` the log runs unsigned with a warning at every start, and with `required` the
  server does not start.
- **Permissions are warned about, never refused.** A file other users can read is reported once per start,
  except `known_hosts`, which holds public host keys.
  A file its group can read is reported only when the server owns it: a Kubernetes Secret reaches a server
  that does not run as root as `root:<fsGroup>` with mode `0440`, whatever `defaultMode` says, and the
  group bit is how the server reads it. OpenSSH draws the same line for a private key.
- **Never inside a KB.** Every commit of a KB stages its whole working tree, so a `credentials_dir` in one
  refuses the start: one that is a KB (it holds `data/index.md`) or lies inside one, one inside a
  `kbs[].path` or a remote KB's clone under `data`, even before it exists, and one below a subdirectory of
  `data`, which is discovered as a KB. A direct subdirectory of `data` is fine: as the credentials directory
  it is not discovered, and neither are `credentials` and `audit`.
  The directory of the audit log (`audit.log`, or `<data>/audit`) and `audit.archive_dir` are held to the
  same rule, since a KB named `audit` would otherwise hold the log and commit it: the start refuses, naming
  the setting to move. Neither is checked in a `--kb` session without data, which has no default log.
- **Upgrading.** The first start of this version copies into the directory the secrets that the removed
  settings point at: `git.ssh_key`, `kbs[].ssh_key`, `git.known_hosts`, `kbs[].known_hosts`,
  `git.token_dir`, `sops.age_key_file`, `sops.age_key_dir`, `kbs[].sops_age_key_file`,
  `kbs[].artifact_signing_seed` and `audit.key_seed`, and their `CARTOGRAPHER_*` variables. A file is
  copied, never moved, links followed (a directory mounted from a Kubernetes Secret holds links); an inline
  value is written; every known_hosts goes into the one file; a file already in the directory is kept. The log names settings and files, never a value. What cannot be written (a
  read-only directory) is logged as the file to create by hand, and the server YAML is not realigned until
  every secret is in place, so it still holds them at the next start.

### Bootstrapping a KB from a git remote (replaces the init container)

Every `kbs: [{remote: ...}]` entry (YAML) or `CARTOGRAPHER_KB_REMOTES` (env, CSV of URLs) is
cloned by `cartographer serve` at startup into `<data>/<name>` (name derived from the last segment
of the remote, without `.git` — or the explicit `kbs[].name`, if set, see D53 below), **only if
the destination doesn't already exist** — an existing clone is left untouched: the existing
sync that follows the remote handles fetch/push on subsequent writes
(`cmd/cartographer/bootstrap.go`). Requires `--data`/`CARTOGRAPHER_DATA`/`data:` as the destination
directory for clones. A remote that is still empty clones to a branch with no commit, named by the
host's git (often `master`), and the server does not mount it: `serve` never creates a KB (D906), so it
fails naming `cartographer kb create`. `kb create <name> --remote <url>` is what fills an empty remote: the KB is
pinned to `main` and given its initial commit, which it pushes as the remote's first branch. From then on writes follow the remote's
default branch (`concurrency.md` §How changes land, D264), unless the `settings.yaml` committed on that
default branch sets `branch` (D335, D903): a fresh clone is then checked out on that branch, and the first
push creates it on the remote if it is missing. An existing clone is never switched.

The clone is atomic (D348): it lands in `<data>/.<name>.cloning` and is renamed to `<data>/<name>` only when complete, so a process killed mid-clone leaves no half-clone that the next start would take for a finished one. A half-clone left at `<data>/<name>` by an older binary is not detected (an existing clone is never touched): remove that directory.

`GIT_SSH_COMMAND` for the clone is built from `default.ssh` and `known_hosts` in the credentials
directory (`ssh -i '<key>' -o 'UserKnownHostsFile="<known_hosts>"' -o StrictHostKeyChecking=yes`, quoted so a
path with spaces or a Windows one survives the shell git runs it with); if
`GIT_SSH_COMMAND` is already present in the environment, it wins over `default.ssh`. This removes the need for
a separate Kubernetes init container for the initial clone.

**Git identity: the writer commits (D904, D950).** A commit with a known writer has that writer as author
**and** committer: a forge with a membership push rule checks both. `<host>` — the machine's name (a pod's
hostname in Kubernetes, reduced to what an email domain accepts) — is recorded in the `Cartographer-Host`
trailer, so the history still says which instance recorded a commit. The author of a write is whoever wrote
it: the `Cartographer-Author: Name <email>` header of the request, else the identity configured for the KB,
else `<token id> <cartographer@<host>>`; every write also carries `Cartographer-Host` and `Cartographer-Token:
<token id>` trailers, the latter unforgeable (see [control plane](control-plane.md)).

The identity the server commits as is a **server setting**, because it is the machine's and not the KB's (a
KB's `settings.yaml` is committed and shared by everyone who writes to it): `git.identity: "Name <email>"`
for every KB, and `git.identities: {<kb>: "Name <email>", default: "Name <email>"}` per KB name (the entry
`default` for the KBs without one; a present empty entry means that KB has none). It is the author and the
committer of what has no writer of its own — the unattended repair, the `external changes` commit, a sync
merge, conflict resolution — and of a write whose client sends no valid header (Kiro's MCP config has no
headers). Without it the server identity `Cartographer <cartographer@<host>>` applies. An invalid value is
dropped with a warning. A sync that rebases local commits gives each its author back as committer, and
re-commits one the server made before the identity was configured as that identity (`gitx.RealignCommitters`).
`git.author_name` and `git.author_email`, the 0.24 keys, are read as `git.identity` and moved to it by the
realignment; the other `author_*`/`committer_*` keys are removed settings (a file that still has them loads
with a warning). The git config of the machine, `GIT_AUTHOR_*` and `GIT_COMMITTER_*` in the environment do
not influence any of it. A KB's own SSH key, `<kb>.ssh`, and its token helper (`<kb>.token` or
`default.token`) **override** the server's process environment (the reverse of the "environment wins" rule that
applies to `default.ssh` and `GIT_SSH_COMMAND` above) — see `internal/gitx.runGitEnv`.

If your forge enforces author or committer push rules (GitLab `member_check`), the server identity and the
token-id author are not forge accounts and the push is refused: set `git.identity` (or `git.identities`) to
a real account. Commits already pushed under another identity stay as they are.

**Explicit KB name and its credential files (D53, D905)**: `kbs[].name` fixes the
KB's name (overrides the name derived from remote/path) — it's the name used everywhere: the HTTP
endpoint (`/mcp/<name>`, see below), the token scope (`kb:<name>:r|rw`), the clone directory (`<data>/<name>`), and the
KB's files in the credentials directory (§Credentials directory). With an `http(s)://` remote, if
`<name>.token` or `default.token` exists there (content: the token, trimmed), the server uses it as the HTTPS
credential for clone/fetch/pull/push of that KB, injected via `credential.helper` in the
per-process environment (`GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0`, D53): the token
never appears in argv, in the remote URL, or in `.git/config` — only the file's *path* is
embedded in the helper, which reads it (`cat`) whenever git invokes it. `ssh://` remotes use
`<name>.ssh` or `default.ssh` (above), and the KB's SOPS key is `<name>.age`, else `default.age`.

### MCP tool-name prefix (removed)

Tools are **never prefixed** (D288): an agent client gets one routed entry whose tools carry
bare names, and one entry cannot collide with itself. The knobs that once prefixed them
(`kbs[].tool_prefix`, `mcp.tool_prefix_mode`, `CARTOGRAPHER_MCP_TOOL_PREFIX_MODE`) and the
topology switch (`mcp.mount_mode`, `CARTOGRAPHER_MCP_MOUNT_MODE`, `--mount-mode`) were
deprecated by D288 and **removed by D325**. A config that still carries them loads and the
keys are **silently ignored** (no warning); the `--mount-mode` flag and the env vars are
gone, so a launcher still passing `--mount-mode` now fails with an unknown-flag error.

*Upgrading from a prefixed deployment:* tool names change, and so do the MCP entry names (a per-KB
entry such as `cartographer-kb-a` with a prefixed tool name becomes the single `cartographer`
entry with the bare name). The next `cartographer sync` rewrites the entries and the generated
instructions block; **hand-written tool citations inside skill bodies, and client permission rules
naming the old tools, are not rewritten** and must be updated. `readOnlyHint` auto-approval (D76)
is unaffected. `cartographer kb rename` never changes a tool name.

### Environment variables

Every startup option has a corresponding environment variable (the CLI flag takes precedence):

| Variable | Equivalent flag | Description |
|---|---|---|
| `CARTOGRAPHER_CONFIG` | `--config` | Path to a YAML config file (see §Configuration: flags, env, YAML) |
| `CARTOGRAPHER_KB` | `--kb` | Folder(s) to mount as they are, comma-separated. No remote, so no sync: for development and tests (D903) |
| `CARTOGRAPHER_KB_REMOTES` | — | Git remotes to clone into `--data` at startup, comma-separated (see §Bootstrapping a KB) |
| `CARTOGRAPHER_DATA` | `--data` | Directory whose direct subfolders are separate KBs (auto-discovery) |
| `CARTOGRAPHER_CREDENTIALS_DIR` | — | The credentials directory, default `<data>/credentials`: every secret file, found by KB name (D905, see §Credentials directory). |
| `CARTOGRAPHER_HTTP` | `--http` | HTTP listen address, e.g. `:39273` |
| `CARTOGRAPHER_TOKENS` | `--tokens` | Bearer tokens, comma/whitespace-separated. Each entry is either a bare `token` (admin, full access to every KB) or `token\|scope1;scope2` with per-KB scopes `kb:<name>:r\|rw` (scopes separated by `;`, never by spaces/commas, to avoid colliding with the between-entry separator). E.g. `admintok, readtok\|kb:wiki:r, writetok\|kb:wiki:rw;kb:notes:r` (D44). |
| `CARTOGRAPHER_AUTH` | — | Explicit auth toggle (see §Auth) |
| — | — | Fine-grained roles have no env form: `auth.roles` is YAML-only, because a rule is a structured object (see §Fine-grained roles). |
| `CARTOGRAPHER_AUDIT_LOG` | — | Path of the audit log's JSONL file, which is always on (D905); empty keeps it at `<data>/audit/audit.log`. Its signing key is `audit.key` in the credentials directory. |
| `CARTOGRAPHER_SERVER_URL` | — | **Client** (not server): default server URL for `cartographer connect` on the client machine when no `.cartographer.yaml` exists yet. Precedence: existing yaml > env > `http://127.0.0.1:39273/mcp` (D64, `internal/clientconfig.Default`). |
| `CARTOGRAPHER_WEB_ENABLED` | `--web-enabled` | Serve the embedded read-only Atlas UI at `/ui/` and its JSON API at `/api/ui/v1` in HTTP mode. Default `true`; `false` registers neither route, so both answer 404 and the HTTP surface is what it was before the UI. Stdio mode never serves it. D227, → `control-plane.md` §Read-only UI API. |
| `CARTOGRAPHER_UPDATE_CHECK` | `--update-check` | YAML `update_check`. Look up the latest release (GitHub release list) 30 s after start and every 24 h, cached in the data dir's `.cartographer/update-check.json` (the user cache dir without a data dir), and report it as `latest_version` in `/health` and `kb_status` when newer. Default `true`; a failed lookup is silent and retried after an hour instead of 24 h, and a `dev` build never checks. D254. The same switch gates the UI API's release history (`GET /api/ui/v1/releases`), a second GitHub call made on demand (§The embedded Atlas UI, D862). |
| — | — | Removed in D906, each warned about and ignored: `CARTOGRAPHER_TOOLS_PROFILE` and `--tools-profile` (the core set is fixed), `CARTOGRAPHER_MCP_ALLOWED_ORIGINS` (the same-origin rule is fixed, → `transport-auth.md` §Origin) and `--init` (`serve` never creates a KB). |

**`CARTOGRAPHER_AUTH`** — three modes:

| Value | Behavior |
|---|---|
| `false` / `0` / `no` / `off` | Auth disabled (e.g. a local service on loopback) |
| `true` / `1` / `yes` / `on` | Auth mandatory — fatal error at startup if no token is configured |
| unset | Auto: enabled if tokens are present, disabled otherwise |

#### Example: native local service (launchd/systemd/Scheduled Task, auth off, single client)

The local mode (D73) uses the binary already installed by `install.sh` as a **user service**:

```bash
cartographer service install                 # generates config + plist/unit, starts the server
cartographer service status                  # binary, config, and the observable state of the local service
cartographer service start|stop|restart
cartographer service uninstall               # removes the service; config and data remain
cartographer kb create <name> --remote <url> # scaffolds a KB in the data dir, pushes it to <url> (D85, D134)
cartographer kb clone <remote>               # mounts an existing remote KB in that data dir (D97)
cartographer kb list                         # what is on disk, and what the server actually serves (D173)
cartographer kb rename <old> <new>           # renames the mount point: directory + kbs[] entry + alias in the server state file + client bindings (D177, D754, D772, D906)
cartographer kb unmount <name>               # retires a KB locally: kbs[] entry + state-file aliases pointing at it + client bindings, clone parked or deleted (D761, D772)
```

`service install` (idempotent: re-running it rewrites the plist/unit and restarts):
- generates `~/.config/cartographer/server.yaml` — on Windows `%APPDATA%\cartographer\server.yaml` — **only if it doesn't exist** (`--config` for a different path; `--data`, default `~/cartographer-data`, and `--http`, default `127.0.0.1:39273`, are only used at generation time — if the config already exists they are ignored with a warning: edit the file and run `service restart`). For a loopback `--http` the generated file sets `auth: {mode: "off"}` explicitly: the clients `setup`/`connect` configure for a local server send no token, and with the default `auto` a `CARTOGRAPHER_TOKENS` exported for a remote server and reaching the service's environment would turn authentication on and reject them all with 401. The mode and the tokens are separate settings, so the pinned `off` holds whatever the environment carries; `CARTOGRAPHER_AUTH=on` (env beats YAML) or editing the file still enables auth deliberately, and `serve` logs the tokens it is ignoring. A non-loopback `--http` keeps `auto`, where env tokens are the protection ([D268](decisions/D268-the-local-service-pins-auth-off.md));
- creates the configured data dir if it doesn't exist yet (D83), so a fresh install never leaves `serve` pointed at a missing directory;
- macOS: LaunchAgent `~/Library/LaunchAgents/com.cartographer.serve.plist` (`KeepAlive`, logging to `~/Library/Logs/cartographer/server.log`). The plist's binary path prefers a stable Homebrew symlink (`/opt/homebrew/bin/cartographer` or `/usr/local/bin/cartographer`) over the versioned Caskroom path, so it survives `brew upgrade` without a re-install (D83);
- Linux: systemd user unit `~/.config/systemd/user/cartographer.service` (log via `journalctl --user -u cartographer`; on a headless host, `loginctl enable-linger <user>` is needed for the service to survive logout);
- Windows: a **per-user Scheduled Task** `\Cartographer\Serve`, defined by `%LOCALAPPDATA%\cartographer\tasks\serve.xml` and registered with the PowerShell `ScheduledTasks` cmdlets — never `schtasks.exe`, whose printed state is localised. It starts at **its owner's** logon — the trigger names the installing user, because a logon trigger for any user needs administrator rights to register and a standard user would get `Access denied` — restarts on failure (3 attempts, one minute apart), and logs to `%LOCALAPPDATA%\cartographer\Logs\server.log` through `serve --log-file`, because a task action has no equivalent of the plist's `StandardOutPath` and Windows has no journald. Not an SCM service: that would be per-machine and would need administrator rights, while this needs none ([D217](decisions/D217-the-native-service-on-windows-is-a-per-user-scheduled-task.md), amending D73). The task's binary path is the one `service install` ran from — `%LOCALAPPDATA%\Cartographer\bin\cartographer.exe` for an `install.ps1` install — which every `install.ps1 update` replaces in place, so unlike the Homebrew Caskroom path on macOS it needs no stable alias. The definition overrides four Task Scheduler defaults that would each stop a long-running server: the 3-day execution time limit, the two battery settings, and stop-when-no-longer-idle.
  Read fallback for the config: if `%APPDATA%\cartographer\server.yaml` does not exist and `<home>\.config\cartographer\server.yaml` does, that one is used, so a machine configured before Windows was supported keeps working — the existing file keeps governing the service and nothing is moved or copied. A **new** config is generated at the `%APPDATA%` location.
- sets a `PATH` in the plist (`EnvironmentVariables`) and the unit (`Environment=PATH=`), but **not** in the task definition, whose format has no environment block — a Scheduled Task inherits the user's own environment, where the machine and user `PATH` already contains the directories installers put there (`install.ps1`'s among them), so the gap this closes on unix does not exist there: the directory of the installed binary first, then `/opt/homebrew/bin`, `/usr/local/bin`, `/opt/local/bin`, then the platform default. A service manager does not pass the interactive shell's `PATH`, so without this a Homebrew-installed `sops` is invisible to the server and every secret resolution fails with `sops binary not found in PATH` — from the definition `service install` itself generated (D156). **An already-installed service keeps its old definition: re-run `service install` to pick this up.**

`stop` leaves the job registered and `restart` works after it. On macOS `stop` disables the job (to defeat `KeepAlive`) and sends `SIGTERM` rather than `bootout`, which unregisters: with `bootout`, `stop` followed by `restart` failed with `Could not find service … in domain` and only `start` worked, which reads like a broken installation. `restart` falls back to `start` when the job is not registered, so a job booted out by an older version stays restartable after the upgrade; `uninstall` still uses `bootout`, because removing the definition is what uninstalling means (D156). Windows follows the same contract with its own verbs: `stop` stops **and disables** the task (the logon trigger and restart-on-failure would otherwise bring it straight back), `start` and `restart` re-enable it first, `restart` re-registers from the XML when the scheduler no longer knows the task, and `uninstall` unregisters and deletes the definition. `restart` and `uninstall` first end the running server and **wait for it to exit** — the task no longer `Running` and nothing answering on the config's http address — because `Stop-ScheduledTask` returns while the process still holds the port: a start in that window dies on bind, and an unregistered task leaves its process running with the port, `index.db` and `server.log` locked. The stop is graceful first (the shutdown event below, up to 20 s), then `Stop-ScheduledTask` (up to 10 s more). A server that survives both is reported: `restart` still starts the task, `uninstall` still removes the definition and names the process to end ([D266](decisions/D266-a-windows-restart-waits-for-the-old-process-and-proves-the-new-one.md)).

`service restart --wait` (D121) is a **graceful** replacement everywhere, and on Windows that took a mechanism of its own: there is no CLI-deliverable `SIGTERM`, `Stop-ScheduledTask` is a kill, and `GenerateConsoleCtrlEvent` cannot reach a process with no console. `serve` therefore also waits on a named event in the user's session (`Local\cartographer-serve-shutdown`) and triggers the same drain a `SIGTERM` does; the Manager sets it, waits for the server to exit, and then **starts the task again** — a Scheduled Task is not a supervisor, so nothing else would bring back a server that exited cleanly. `Local\` is per logon session, so from another session — `upgrade-repair` or an `auto-patch` apply run over SSH while the server runs on the desktop — the event cannot be opened: the Manager then stops the task with `Stop-ScheduledTask`, waits for the exit, and prints why, since that stop cuts in-flight requests and does not flush pending pushes. The verification that follows only accepts a `/health` answer from a **new** process: `/health` reports `started_at`, and a value equal to the one read before the restart is the old process still draining (D266).

**Per-KB policy lives in the KB, and only there (D746, D906).** `kbs[]` keeps two keys, `remote` and `name`. A KB's behaviour (`usage_stale_days`, `branch`; the write gate and the safe repairs have no key, D907) is set in the KB's own `settings.yaml` (`docs/data-plane.md` §The KB's own settings), the same for every server that mounts it. The former `kbs[]` form of those keys, and `allow_artifact_write`, `allow_template_write`, `mcp_allowlist` and `secrets`, are removed keys: each is ignored with a warning that names the KB file (or says why the key is gone), and dropped when the server file is realigned. A KB declares secrets by holding a sops-encrypted file under `secrets/`; its MCP descriptors are always distributable; artifact writes need only a token with `rw` scope on the KB.

**Configured versus discovered mounts.** A KB listed in `kbs[]` is *configured*; a KB found by scanning `data:` is *discovered* and has **no `KBSpec`**, so every per-KB setting sits at its zero value unless the KB carries a `settings.yaml` (D746, which a discovered KB reads like any other) — no `machine_path_allow_prefixes` — and nothing but adding the entry can change that. A discovered KB otherwise works and looks identical from every client surface, so the mount kind is reported explicitly: startup warns once per discovered KB, `kb_status` reports `capabilities.mount: discovered`, and `cartographer doctor` raises a `capability` finding (D151). `kb create` / `kb clone` mount a KB *discovered*. An existing discovered KB is still fixed by editing `kbs[]`. Dot-directories are never discovered: `kb unmount` parks a retired clone in `<data>/.unmounted/` for that reason (D761).

`kb create` authors the KB's initial commit with the operator's own git identity (`user.name`/`user.email`) when git resolves one, otherwise the server identity (D904); the committer is the same identity (D950). **Before pushing it asks the remote (`ls-remote`).** An empty repository gets the scaffold as is. A repository holding only a forge's initial commit — one branch, one commit, top-level README/LICENSE/LICENCE/COPYING/`.gitignore`/`.gitattributes` files — is adopted automatically: the scaffold commit is rebased on top of it (the forge's files are kept), the KB lives on the remote's default branch whatever its name (D264), and the push is a fast-forward, never forced; a scaffold path that collides with a forge file (e.g. `.gitattributes`) is a refusal, not a merge. Any other non-empty remote is refused with a `kb clone` / kb-import hint. These refusals happen before any push, so the local KB is deleted and the command can be rerun (D710). **If the push fails the scaffold is kept**, not rolled back: the local KB is complete and valid and only the push failed, so the command exits non-zero and prints the `commit --amend --author` and `push -u origin` lines to finish by hand — or the command that discards it (`rm -rf`, or `Remove-Item -Recurse -Force` on Windows), noting that a server started with that data dir would otherwise mount it (D156). If git refuses the initial commit itself (a commit hook, signing without a key), `kb create` reports that error and stops before the push (D265).

Binds to **loopback** by default (`127.0.0.1:39273`) → auth is off (pinned in the generated config, D268) without exposing anything on the network. With an empty (or missing — D83: `serve` creates the directory and treats it as empty rather than failing) data dir the server starts with 0 KBs — `/health` is still up (liveness: `status:"ok"`) but `/ready` reports `503 {"ready":false}` (D84, §Observability): `cartographer kb create <name> --remote <url>` scaffolds a subfolder KB (D85; `serve` itself never creates one, D906) and pushes it to the empty repository at `<url>` (or builds it on the one commit a forge writes at project creation, D710), which becomes its `origin` — the remote is mandatory, `--no-remote` being the explicit opt-out for a local-only, non-durable KB (D134) — while `cartographer kb clone <remote>` mounts an existing OKF remote there (D97; `service install` itself prints a hint pointing at creation if it starts with 0 KBs mounted), or add `kbs:` entries to clone remotes (§Bootstrapping a KB) — either way, `service restart` (or `kb create --restart` / `kb clone --restart`, which do this for you and wait for the server to report healthy again) is what makes the new KB visible.

**The `kb` commands act on the LOCAL server's data dir** (D173). They take `--config <path>` to name the server config to read `data:` from, mirroring `service install`/`service status`: without it a service installed at a custom config path is invisible, and the command reports `KB "x" mounted at …` about a directory no running server reads. On a machine whose `.cartographer.yaml` points at a **remote** server they refuse to run, since mounting a KB on that server is an operation on its deployment — `--local` is the explicit opt-out, and `--data <dir>` (naming the target outright) bypasses the question.

`kb clone` is bounded: `--timeout` (default 120s) caps the whole operation, `GIT_TERMINAL_PROMPT=0` and, for ssh remotes, `BatchMode=yes -o ConnectTimeout=10` make git fail instead of blocking on a credential or host-key prompt, and progress is streamed as it happens. Host-key *policy* is left to the operator's ssh config: `accept-new` would trade a hang for a silent trust-on-first-use decision. Recognised failures (DNS, SSH key, host key, repository not found, authentication) are reported with a remedy, and an interrupt removes the partial clone it created — never a directory that was already there. A pre-existing `GIT_SSH_COMMAND` is never overwritten.

`kb rename <old> <new> [--data <dir>] [--config <path>] [--restart]` renames a KB's **mount point**: the directory, the matching `kbs[]` entry when there is one, and an alias `{from: <old>, to: <new>}` in the server state file `<data>/.cartographer/state.json` (D177, D772, D906), together or neither: if the entry cannot be rewritten, the directory is renamed back and the alias removed. The alias is written whether or not the KB has an entry — a discovered KB has none, and no entry is created for it — and even with no server config, since the file lives under the data dir, so a routed `?kbs=<old>` from a client that has not synced yet still reaches the KB after the next restart (see `transport-auth.md`). A `kb_aliases` list the server YAML still carries is moved into the state file by the command, and the key leaves the YAML. The list stays flat and single hop: renaming `b` to `c` after `a` to `b` leaves `a: c` and `b: c`, and renaming a KB back drops the alias of the restored name. It is offline and local — it never contacts a server, a client or a git remote, and the git `origin` is untouched: renaming a mount point is not renaming a repository. A cross-filesystem move fails explicitly rather than falling back to a recursive copy, which would silently change the ownership and timestamps of a git repository.

Before moving anything it reports what else the name is an identity for. Auth scopes (`kb:<old>:r|rw`), role rules, client `signing_keys` pins, `mcp_approvals` entries and `identities` entries (D904) are **listed, never rewritten**: they are configured out of band, and silently editing an operator's auth configuration would be worse than telling them what to change. This machine's client bindings that select the KB by name (`clients.<provider>.kbs`, `workspaces.<provider>[].kbs`) **are rewritten** to the new name, in place, when the client points at the local server — otherwise every `sync` would abort on a stale selection (D754); a client pointed at another server keeps its bindings, which name that server's KBs. Clients' MCP entries then reconcile on the next `cartographer sync`.

Two entries that could both be the KB is a refusal, naming them: guessing there silently detaches a KB from its configuration. **No** entry is not a refusal — a KB created by `kb create` or found by discovery legitimately has none, and renaming its directory is then the whole job.

`kb unmount <name> [--delete-clone] [--force] [--dry-run] [--data <dir>] [--config <path>] [--restart]` is the inverse of `kb create`/`kb clone`, offline and local like `kb rename` (D761). It refuses when the clone cannot be proven pushed — uncommitted changes, commits ahead of `origin/<branch>`, no upstream or no `origin` (it runs `git fetch` first) — unless `--force`. It then writes `server.yaml.bak-<UTC timestamp>` and removes the matching `kbs[]` entry in place (comments kept; a discovered KB has none) together with the state-file aliases that point at the KB or start at its name (an alias records where a KB went, not who owns a name, so a different KB later mounted under it must not be reached by an old URL, D772), drops the KB from this machine's client and workspace bindings, and either deletes the clone (`--delete-clone`) or, when it sits under `data:`, parks it in `<data>/.unmounted/<name>`: a clone left in the data dir would be remounted by discovery at the next start. Auth scopes, signing-key pins, MCP approvals and `identities` entries naming the KB are listed, not removed. A failure after the config edit restores it from the backup, and the aliases with it. The remote stays the source of truth: the command prints the `kb clone` line that brings the KB back. Other machines' client URLs still name the KB until they `sync`; the routed endpoint serves them without it (D756).

`kb list [--data <dir>] [--config <path>]` prints one row per subdirectory of the data dir — name, whether it is an OKF KB (`data/index.md`), whether it is a git repository, its `origin` — and a `MOUNTED` column when the server answers `/health`. When it does not, the column disappears and the reason is printed: absence of the signal is not evidence that nothing is mounted. The command writes **nothing**: a missing data dir is reported rather than created, and no repository is opened (which would migrate its git-exclude entry as a side effect).

`service status` uses systemctl-like exit codes: `0` running, `3` installed but stopped, `4` not installed — this is what lets `install.sh update` automatically restart only a running service (see §Client installation).

**What `service status` reports** (D174). It inspects the **local native service** and nothing else: the remote server a client points at is a different question, answered by `cartographer status` and `cartographer doctor`. It reports what it observed, never a verdict on what does not exist:

- `installed` — the plist, the unit or the task definition is on disk. It is a **file**, on every platform, and never a query to the scheduler: `EffectiveConfigPath` reads that same file to recover the `--config` the service was installed with. When it is `false` the output says there is no local service and how to install one, and prints **no health line**: `healthy: false` next to `installed: false` describes an absence, and reads as an outage.
- `loaded` — `launchctl print` / `systemctl --user is-active` / `Get-ScheduledTask` succeeded. On macOS it is false for a job that `stop` left registered but disabled and without a process (D1006), so `status` exits 3 and `upgrade-repair` leaves it alone; a job with a live `pid` is loaded whatever its disabled flag says. On Windows it is false for a task `stop` left `Disabled` or `Ready` (registered, no process), so `status` exits 3 and `upgrade-repair` leaves it alone (D1014); the state is compared inside PowerShell, and a state query that errors or answers anything unexpected reads as loaded, so a live service is never hidden. It does **not** prove the server answers. The JSON field keeps its name, `running`, and `lifecycle` (`not_installed` / `not_loaded` / `loaded`) is the field to read.
- `healthy` — the result of `GET /health` on the address in the server config. It is a verdict only when the probe **ran**: `health_checked` says whether it did, and `health_skip_reason` why not (`config_missing`, `config_unreadable`, `stdio_transport`). An empty `http:` means the server is configured for **stdio**, so there is no endpoint to probe — the default listen address is never substituted, since that would probe an address nothing listens on.
- `ui` — the address of the embedded Atlas UI (JSON `ui_url`, D227), only when the service is installed, configured for HTTP and `web.enabled` is on. A wildcard bind (`0.0.0.0`, `::`) is reported as `127.0.0.1`, because a browser cannot open a wildcard. An absent line means there is no UI, never that it is down — liveness is `healthy`.
- `client` — one context line when the `.cartographer.yaml` on this machine points at a server that is not this local service. A local service alongside a client pointed at a shared remote one is a legitimate configuration, reported as context and never as a warning.

The `--output json` shape is a contract: fields are added, existing fields and the exit codes keep their meaning.

**Data/code separation**: the cartographer repo contains no data. KBs live in the data dir (default `~/cartographer-data`); every subdirectory of it is a separate KB. For multiple users on shared KBs, see the k8s topology below — the server configuration is identical, only where it runs changes; one server process is the sole writer of a given working copy, so several instances mean separate clones synchronizing through the same remote, never two writers on one checkout (`concurrency.md` §Writer boundary).

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
    credentials_dir: /etc/cartographer-credentials
    kbs:
      - remote: ssh://git@gitea.example.com:2222/user/wiki-kb.git
```

`Secret` (the credentials directory, §Credentials directory: one key per file):

```sh
kubectl create secret generic cartographer-credentials \
  --from-file=default.ssh=./id_ed25519 \
  --from-file=known_hosts=./known_hosts \
  --from-literal=audit.key="$(openssl rand -hex 32)"
```

`Deployment` (excerpt — MCP tokens and the credentials from Secrets, never in the ConfigMap):

```yaml
securityContext:
  fsGroup: 2000 # any gid: the process joins it, and the kubelet gives it the Secret's files
  fsGroupChangePolicy: OnRootMismatch # the data volume is not re-chowned at every start
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
      - { name: credentials, mountPath: /etc/cartographer-credentials, readOnly: true }
      - { name: data, mountPath: /data }
volumes:
  - name: config
    configMap: { name: cartographer-config }
  - name: credentials
    secret: { secretName: cartographer-credentials, defaultMode: 0440 }
  - name: data
    persistentVolumeClaim: { claimName: cartographer-data }
```

The image runs as a user that is not root, so the Secret needs `fsGroup`: the kubelet makes its files
`root:<fsGroup>` and adds `0440` whatever `defaultMode` says, and without `fsGroup` a `0400` file is
unreadable to the server. The group bit is not reported as a loose permission (§Credentials directory).
The mount is read-only, so `audit.key` comes in the Secret: the server cannot generate it there, and
without it a `best_effort` log runs unsigned, warning at every start, while `audit.mode: required` does
not start. Back the key up, since the audit log's signatures depend on it. A KB that needs a key of its
own adds `<kb>.ssh`; an HTTPS remote reads `<kb>.token` or `default.token`.

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

A fourth route, `/mcp/routed` (D187, always served since D288), is the one agent clients use: it
serves every mounted KB through one endpoint, advertising the union of the tools exactly once, with
the KB carried as a `kb` tool argument (required for 2+ KBs, absent for one). `?kbs=kb-a,kb-b` on
that URL narrows the connection to a subset; a name the server does not mount is served without it and reported in `kb_status.connection.unknown` (D756), unless it is the old name of a renamed KB (`kb_aliases`, written by `kb rename`), which selects that KB and is reported in `kb_status.connection.renamed` (D772); `400` only when no name resolves. The three routes above stay
as plumbing with their exact behaviour, `tools/list` output included. A `?kb=` on `/mcp/routed` is
`400 conflicting kb selection`. See `docs/transport-auth.md` §One routed topology.

`cartographer connect` and `cartographer sync` write **one** entry per provider against a routed
server, `<server_name> → /mcp/routed`, discovered from `/health`'s `mount_mode`/`routed_path`. A
provider with an explicit KB binding gets `?kbs=<bound, sorted, comma-joined>` in that URL (names the
server no longer mounts are left out); the default binding, every KB the server mounts, writes no
`kbs`. Against a server that does **not** report `routed_path` (pre-D288) the client keeps writing
one entry per mounted KB, `<server_name>-<kb> → /mcp?kb=<kb>`, and a one-KB server keeps the single
bare `<server_name> → /mcp` entry.

### The embedded Atlas UI (D227)

The UI is English whatever the browser locale (labels, dates, times), exported replays included.

With `web.enabled` on — the default in HTTP mode — the same binary also serves:

- `/ui/` — the embedded read-only Atlas. Unknown paths **below** `/ui/` serve the
  shell so client-side routing works; unknown paths outside it stay `404`, which
  is what keeps the fallback from shadowing `/mcp`, `/health`, `/ready`,
  `/clients`, the OAuth metadata or `/api/`. `/ui` redirects permanently to
  `/ui/`, and `/` does the same from outside the auth chain.
- `/api/ui/v1/…` — the JSON the UI reads (→ `control-plane.md` §Read-only UI API). Its Settings page lists every
  server and per-KB setting with its value and origin, read-only (D743, §Configuration above).
  The server's version, in the top bar, opens the release history: a sheet
  with every release, newest first, each changelog rendered as sanitised Markdown, the running release marked
  `Current`, and, above them, the upgrade command when a newer release is known; a cut changelog links to
  GitHub, a disabled check is said so, and the static demo, with no server to ask, says the same.
  `GET /api/ui/v1/releases` is the release history behind the server's version (D862): the server reads the
  GitHub release list when the route is asked (a browser cannot, the CSP is `connect-src 'self'`), up to 3 pages
  of 100 releases, each changelog cut at 64 KiB (`truncated`), and keeps it in memory for an hour, served `stale`
  when a refresh fails. The upgrade command for the install channel comes with it when a newer release is known.
  It obeys the update-check switch (`update_check`, `CARTOGRAPHER_NO_UPDATE_CHECK`, never on a `dev` build): off,
  the route answers `unavailable: "disabled"` and the server makes no call.
  The rail holds six panels: Atlas, Activity, Work, Health, Artifacts and Settings (D818).
  Health shows the lint's findings and nothing else; what a person has to answer or decide is in Work
  (contradictions and gaps, the searches that found nothing under "to answer", the `kb_review` proposals
  under "to decide", D948). Artifacts lists every kind, an empty one as an outline tile where its first
  artifact would go, and has a Secrets tab (a card per Service that uses a value and per sops file, the same
  detail panel as an artifact); Activity has a Retired tab (D820: every deprecated, superseded or archived concept with where it went,
  when and why, read from the root `log.md`, and each journal's harvest state; an archive map wears an "Archive"
  badge in the rail); the command palette's "Include archived" toggle adds archived pages to a search;
  pending sources are Work's "to ingest" column. Settings has three tabs, Client, Server and
  Knowledge Base (no counts on them), and opens on the Client one: the client's own settings first, as the
  viewer's last `sync` reported them (its agents, the workspaces with their isolation), then the KBs as a row
  of pills (the active one picked) and, for the picked one, the client keys that concern it: which of the machine's agents receive it and where (everywhere, or only the folders
  bound to it, through the bridge or over HTTP) and where its placeholders resolve on that machine. Each key
  shows its description and its value; the default, where it was set, the environment variable and the flag
  open on a click. A readable view and a YAML view are switched in the header; the Knowledge Base tab lists the
  KB's own keys and placeholders. Before any client of the viewer has reported, the Client tab shows the keys of
  `.cartographer.yaml` at their defaults (`client_template`, D948). The former
  `panel=services`, `panel=sources` and `panel=paths` links land on Secrets, Work and Settings.

The static bundle is reachable without a bearer token, because it is the page
that asks for one and it carries no server or KB data; `/api/ui/v1` is not, and
is filtered with the caller's own principal. The shell is served `no-cache` and
hashed assets are immutable, under a `default-src 'self'` CSP with no inline
script and no eval. `web.enabled: false` registers neither route.

The bundle has two build-time inputs that only the public Pages build sets
(`web/src/api/static.ts`): `VITE_ATLAS_STATIC=1` (the D745 snapshot, below) and
`VITE_ATLAS_LIVE_ORIGIN` (D800, the origin of the live demo). Unset, which is the
embedded bundle's case, the UI is unchanged: same-origin `/api/ui/v1`, revision
poll every 10 s.

Stdio mode never serves the UI, whatever the setting says.

#### The public static demo (D745)

The documentation site also carries a demo of the Atlas at
<https://beppetemp.github.io/cartographer/demo/>, with no server behind it. `demo/build.sh <out>` makes it:

1. it replays the demo KB, `demo/astronomy`, into a throwaway git repository as commits spread over
   the last five months, so Activity and the growth timeline have a history whenever it runs;
2. it builds the UI with `VITE_ATLAS_STATIC=1`, which makes the API client read JSON files next to
   `index.html` instead of `/api/ui/v1`, and adds a "Demo snapshot" link to the top bar; it also sets
   `VITE_ATLAS_LIVE_ORIGIN` (`DEMO_LIVE_ORIGIN` overrides it, empty turns it off), see below;
3. it serves that repository with the real binary and `demo/export.mjs` saves every answer the
   UI can ask for into `<out>/api/`. Search takes free text, so it is the one request not
   exported: the export writes `search-index.json` and the browser searches it.

**Live source (D800).** `demo/build.sh` also sets `VITE_ATLAS_LIVE_ORIGIN` to the live demo's origin. Once per
page load, before its first API read, the client asks `GET <origin>/api/ui/v1/kbs` (6 s, enough for a Cloud Run
cold start). If that answers, every API read of the session, and the version read, goes to the origin, so the
page shows both KBs, `astronomy` and `guestbook`, and the revision poll runs every 20 s to stay inside the
Worker's free quota (the origin's Worker answers CORS for the Pages origin and caches the reads for 5 s). If it
does not answer, the session uses the exported snapshot exactly as above (astronomy only) and the top bar says
the live demo is unavailable. There is no switching mid-session.

The `docs` workflow runs it after `mkdocs build` and publishes it as `site/demo/`, on every change
to `demo/`, `web/` or `CHANGELOG.md` (that is, on every release). `demo/astronomy` is a
hand-written KB with invented, public content. It is also a small tour of the features: maps with
contracts, journals, templates, a source ledger, contradictions and knowledge gaps, supersede and
archive, services with sops-encrypted secrets, and skills, an agent and a hook. Its lint findings
are kept on purpose, so Health has something to show. The secret values are fake, and the age key
that encrypts them is not in the repository.

#### The hosted MCP demo (D751, D800)

The same KB is served live at `https://cartographer-demo.beppetemp.com/mcp`, so an agent client can try the
read tools with nothing installed, and leave a message in a guestbook. It is a stateless Cloud Run service
(`europe-west1`, scale to zero, one instance at most, so a single writer) running the released image plus two KBs
(`deploy/demo/Dockerfile`):

- `astronomy`, the replayed `demo/astronomy` baked into the image (`demo/replay.sh` is the replay
  `demo/build.sh` shares), read-only;
- `guestbook`, seeded from `demo/guestbook/` in a private git remote whose repository must be named
  `guestbook` (the KB name is the remote's basename). The image's entrypoint (`deploy/demo/entrypoint.sh`)
  clones it into `CARTOGRAPHER_DATA` with a 20 s timeout and the same per-KB credential helper the server uses; on
  any failure it logs one line and the server starts with astronomy alone, because a remote that fails to clone is
  fatal inside `serve`. Each message is one commit, pushed inside the request
  (the server sees `K_SERVICE`, which Cloud Run sets, and pushes inline, D903), so Cloud Run's CPU throttling
  cannot strand a push.

Auth stays `on` with two tokens: a read token `kb:astronomy:r;kb:guestbook:r` and a write token
`kb:guestbook:rw`. A Cloudflare Worker forwards the hostname to the `*.run.app` URL (Cloud Run routes by Host),
maps the public `/mcp` onto the routed mount `/mcp/routed`, and answers 404 under `/.well-known/`, so no client
starts an OAuth flow and a client connects with the URL alone, a claude.ai or ChatGPT connector included (D789).
With both KBs mounted every tool takes a required `kb` (an enum of the two); with the guestbook missing the tools
carry none. The Worker sets the read token on every request, replacing the client's, and attaches the write token to
exactly one call: a `concept_new` from the `message` template on `kb: guestbook` that passed moderation. Anything
else, a write tool added to the server tomorrow included, reaches the server with the read token and is refused by
its scope check. Moderation (`deploy/demo/worker.js`, pure functions tested by `make demo-worker-test`): a 16 KB
body cap; the kill switch `GUESTBOOK_OPEN`; exactly the vars `client` (1 to 60 characters, one line) and
`message` (1 to 500 characters, six lines at most), no links, markup or control characters; three attempts per IP
per hour and 100 a day overall, counted in a KV namespace; a Llama Guard 3 verdict from Workers AI within 5 s that
fails closed (an error, a timeout or an exhausted quota refuses the message); then the Worker assigns the id
`messages/<UTC date>-<6 base36>`. A refusal is a JSON-RPC result with `isError`, so the visitor's agent reads and
relays the reason. After an accepted message the Worker POSTs a Standard Webhooks signed JSON to `NOTIFY_URL`
when one is configured; what receives it is the operator's business. A message is removed with a `git revert` in
the private repository.

The edge cache and the free-tier limits: `GET /api/ui/v1/*` and `/health` are cached at the edge for 5 s keyed on
the URL (every visitor shares the read token) and answered with CORS for the Pages origin (`ATLAS_ORIGIN`); one
rate-limiting rule applies per client IP before the Worker; the Pages Atlas polls every 20 s instead of 10. The
whole demo stays on free tiers (Worker 100k requests a day, KV 1,000 writes, Workers AI daily allocation, Cloud
Run monthly free tier with a 5 EUR budget alert); the table of what happens at each limit is in
`deploy/demo/README.md`. The `*.run.app` URL stays reachable around Cloudflare, but only with a token, and neither
is published; the instance cap is the backstop. `/ready` needs the token once auth is on, so the Cloud Run probes and the
post-deploy check use `/health` (public, carries `ready`). Terraform, bootstrap, moderation and token rotation:
`deploy/demo/README.md`. The `demo` workflow (`.github/workflows/demo.yml`) rebuilds and redeploys it on
every release, then checks that the hostname initializes an MCP session without a token and warns (it does not
fail) when `tools/list` no longer lists the guestbook, so a change to the read tools or to `demo/astronomy`
reaches it at the next release; `make demo-smoke` checks the image locally against a bare git repository.

An open Atlas follows the KB live (D337): while the tab is visible it asks
`/api/ui/v1/kbs/<kb>/revision` every ten seconds and, when the answer changes,
refetches the open view in place — graph, concept, findings and lists — without
a reload or a blank frame. The graph keeps every node it already has: new
concepts appear beside a neighbour, removed ones go, and every change —
nodes or links — gives the whole network a small knock that fades with
distance, while the camera and its slow turn carry on undisturbed. A hidden tab does not poll and checks once on
return. A caller that cannot see the whole KB gets no revision, so its Atlas
does not refresh by itself.

Behind an SSO reverse proxy that protects `/ui` and `/api/ui` only (leave `/mcp`
on bearer auth) and injects a read-only bearer, the Atlas opens without the token
prompt. The UI sends same-origin cookies so the proxy session reaches the API
(D308). The origin rule of `/mcp` is fixed and keeps that setup working (D906): the
proxy serves the page and the API from one origin, so a request's `Origin` has the
`Host` it arrived with.

The Atlas draws its graph in 3D, as a live force-directed network (D234, D235): it breathes and turns slowly on its own; drag to orbit, zoom with the
wheel or a pinch, click a concept to select it (nodes are not dragged). A *Motion* toggle, remembered per browser,
pauses the drift, the slow panorama and the signals a selection sends along its
links; `prefers-reduced-motion` starts it paused. The simulation advances at a
fixed 60 Hz whatever the display's refresh rate, so the motion has the same
pace on a 60 Hz and a 120 Hz screen (D736). A network whose simulation costs
more than a quarter of a 60 Hz frame per tick (on an Apple M5, from about 1,500
concepts) rests its drift: it settles and stops, as with motion off, while the
panorama and the signals still run; a smaller Map opened in its place breathes
again. The view draws only when something moves or changes, at most about 60
times a second whatever the display's refresh, so an untouched graph at rest
costs no drawing at all. There is no 2D view: when
the WebGL context is lost the graph area says so. The view draws up to 5,000 concepts (the graph API's ceiling). A browser without WebGL
gets the concept list, search and inspector, with a note where the graph would
be. The UI follows the system's light or dark scheme until the viewer picks one
(D233). The reading panel is resizable: drag its left edge, or focus the edge
and use the arrow keys (Shift for larger steps, Home/End for the extremes,
Enter or a double-click for the default 420px). The width is remembered per
browser (`cartographer.inspector.width` in localStorage) and clamped between
320px and the smaller of what leaves 280px of graph beside the rail and what
the concept's prose can use — its 68ch reading measure plus padding (D281).
Widening the panel reframes the selected concept in the graph it still shows;
on a narrow screen the panel is a sheet and has no splitter (D239).

Opening a KB, or switching to another, grows the graph from a seed at the
orbit's centre: the nodes burst outward to their places, the edges fade in
behind them, and the camera settles on a framing fitted to the KB's size,
whatever it is. The graph is drawn as soon as it and the KB's artifacts arrive:
the rail's Maps and counts fill in on their own, since they wait on the whole-KB
lint (D736). A view seen before in the session is shown at once from memory
while it refreshes. Choosing another Map of the same KB does not regrow it: the
concepts both views draw keep their places, those leaving shrink away, those
entering grow in beside a neighbour already placed, and the camera eases onto
the new set (with reduced motion the swap is instant). Leaving the Atlas for
another panel and coming back shows the graph settled where it was left, camera
included. Selecting a concept, or closing the reading panel, fades the rest of
the graph out or back in while the camera moves, rather than recolouring it at
once. Every move the reader starts -- a camera flight, the Map contract sliding
in with the canvas narrowing beside it, a folded group turning its chevron, a
count running to its new value -- is timed on the one `--motion-*` scale of
`web/src/styles/tokens.css`, the JavaScript ones included, and none lasts
longer than `--motion-deliberate` (520 ms); with reduced motion each is
instant. The *Replay growth* control replays how the KB
grew (D367): the concepts appear in the order their files were first committed,
those born in one commit along their links, in 8 to 20 seconds whatever the
KB's size, under a timeline with one bar per day that saw concepts born. It can
be paused, dragged or stepped with the arrow keys, keeps every node in frame,
hides the legend and the camera controls while it runs, and stays open at the
end until closed.

*Export video*, a button of the replay's control bar, renders that replay as a
clip to share (D691, which replaces D677's recorder), entirely in the browser
and in the background: the Atlas stays usable while it runs, a hidden tab does
not slow it, and switching KB or Map does not end it. A small progress pill
("Exporting video 42 %", Cancel) shows how far it is, and the file downloads
when it is done. The frames are not captured from the screen: a second scene,
in a detached container, is advanced by a virtual clock of exactly 1/60 s and
drawn at twice the export size, then downsampled, so every frame is rendered
and encoded at 60 fps, sharp on thin edges. Frames go through WebCodecs and
the mediabunny muxer: H.264 High (level 4.2) in an MP4 where the browser
encodes it (Chrome and Edge, Safari), else VP9 in a WebM (Firefox, Chromium
builds without H.264), which the dialog says; no WebCodecs, no button. Sizes
are 16:9 (1920×1080), 9:16 (1080×1920, the default) or 1:1 (1080×1080), at
at most about 8 Mbit/s for 1080p (variable rate, a keyframe every 2 s), so a
clip stays under some 25 MB and is far smaller on a small KB. The clip opens on a 1 s card with
the Cartographer mark and the KB name (frame 0, so the thumbnail a messaging
app shows is the logo, not a black frame), then the replay at its own pace
(8 to 20 s) with the KB name, the date being replayed and the concept count
(no node labels), 1.5 s on the full graph, and a 2.5 s end card.

A selected concept is the orbit's pivot: dragging turns the graph around it,
while it stays centred in the strip the panels leave visible, and everything
outside its neighbourhood recedes. The skills, agents and hooks that reference
concepts are drawn as diamonds linked to them; clicking one opens it on the
Artifacts panel (D286). The legend colours nodes by community or by Map, never by
lint findings: on a real KB a finding colour painted most of the graph one
colour, and a KB's health is read on the Health panel (D307). The legend folds to its
switch and counts, remembered per browser (`cartographer.panel.legend`). Ctrl/Cmd+K searches titles and ids at once and, after a
pause in typing, the full text through the `search` tool, showing the excerpt
that matched; it is the way to a concept, and there is no concept list beside
the canvas (it also hides two easter eggs, gone on a reload). The brand in the top bar returns to the atlas on *All*, nothing selected; the server's version
sits beside the theme switch (a button: it opens the release history), and the connection status appears only when the
server is unreachable. The rail lists the KB's Maps and Journals in two
groups, *All* first, each with its count; a click always opens the Atlas on
that Map, and the rail marks a Map only while the Atlas is open (Work, Activity
and Health filter by Map inside the page).

The reading panel folds the concept's outline into *On this page*, drops the
body's leading title (the header shows it) and hides a trailing links section
that lists nothing the *Links* tab does not already show (D287). The *Links*
tab lists links by title — *Links to*, *Linked from*, and *Used by* for the
artifacts that reference the concept.

Activity, Work and Health are reading panels built from one page kit (D339):
a header that says the answer in a sentence with its count in bold (long
explanations behind a *?*), an open hero with no box around it — large
figures, a chart where there is a series, and filters as pills (a pressed one
lights up in its Map's colour) — then dense rows. They share one width, Map colours mark every
row, and nothing is said twice: a count in the title is not repeated in the
band, a section with nothing in it is left out rather than shown empty, and an
empty answer is said in one line with a check.

The **Activity** panel is `changes_since` for the reader: the concepts changed
in the last day, week, month, quarter, year or ever (up to 500). It tells the
reader's changes from the server's upkeep: a concept is *edited*, *new* or
*reorganised* (moved, superseded, removed) by its latest content change
(`last_edit_at`, `added`), and one only the background repair touched is
*maintenance*, hidden unless the reader shows it. The band counts edited, new
and reorganised concepts (and authors, when more than one), names the concept
worked on most, and draws one bar per day of the window, stacked by kind. It
offers *Where* (a bar split by Map, one row per Map) and *Who* (one row per
author) as filters. Below it the timeline groups the concepts by the day each
one last changed, newest first, one row each on a rail in its Map's colour:
time, title, Map, the kind of change when it is not a plain edit, the first
recorded reason (upkeep reasons dropped when there is another) and the
authors. Changing the window keeps the current answer on screen, dimmed,
until the new one arrives. A row opens the concept on the atlas.

The **Work** panel is where a person answers or decides (D302, D948). It is `work_list` for the reader:
the concepts in an open phase, the concepts that are work by their `reason` (open contradictions, open gaps,
pages past their `review_after`) and the unchecked items, to which it adds what has no page of its own, the
searches that found nothing and the doctor's proposals. The band filters by Map (*Where*), by column and by
staleness, each with its count; a search box filters titles, items and proposals. The board lays the work out by
column, in the order work moves: *to resolve* (an open contradiction: `commit_gate` blocks until someone decides
which page holds), decision-needed, blocked, in-progress, open, *to answer* (the open gaps, `missing_context` and
`open_question`, and the searches of `kb_status.search_misses` that found nothing; one that finds something now
leaves the column and is named under *Found since*, D319), proposed, *to review* (a page past its `review_after`
that is neither open by its status nor finished), *to decide* (the proposals of `kb_review`, `observation` items
included, `GET /api/ui/v1/kbs/<kb>/maintenance/review`), *to ingest* (pending sources) and *unchecked items* (a
closed concept listed only for them); any other status a map's `open_statuses` names follows. The other layout is
one column per Map, busiest first, with the same cards. A card shows its Map, title, type (a contradiction its
kind) and age (stale ones flagged), expands to its items, and opens the concept on the atlas; a search opens
nothing. It is read-only: work changes through the agents' write tools. The pending sources, `kb_status` and the
review list are each optional: without one (a caller that cannot see the whole KB has no `kb_status`) the board
shows the rest.

The **Health** panel (D338, D365, D947, D948) shows what is wrong with the pages and nothing else: the lint's
findings. What a person has to answer or decide is in Work. Two buttons in the header switch its views, *Issues*
and *What it checks*.

*Issues*: the title counts the kinds of issue found ("Cartographer found 25 kinds
of issues"), the line under it every occurrence and how many pages carry one.
The **page field** draws one square per page, clustered by Map under the Map's
hue. A page with a problem takes the colour of the category of each of its problems (up to four, drawn as halves,
stripes or quadrants); a page without one is dark (the legend names the categories present, and *fine*). A lit
square opens its page, a Map's name filters the page to it (`hmap=` in the URL, one whole-KB
`GET /api/ui/v1/kbs/<kb>/lint` filtered by path, counts over the unfiltered
findings) and pressing it again returns to the KB. Under it the issues are grouped by the category of the check,
in the catalog's order (pages, templates, validity, links, values, maps, artifacts, the KB itself), each category
with its own colour, the same in the squares, the legend and the bars, and folding: one card, a row per category
with its count of kinds, only the first open. A lint finding is a problem and has no
severity (D947): there are no bands of problems and suggestions, no count by severity and no trend. The doctor's
`kb_review` items, observations included, are decisions, and the open contradictions too: both are in Work.
In each category one row per kind, the heaviest first: its name, a bar for its weight against the heaviest row, and
its count in its own unit (pages, maps, files, terms, templates…); a kind
Cartographer fixes by itself says *fixes itself*. Hovering a row lights its pages
in the field. An open row says *What it is* and *To
fix*, then one line per case with its page count; the pages unfold on click,
named by their last segment, and a file that is no page is named without a link.
The rail does not filter here: a Map click
opens the Atlas on it, and the Type and Status filters step aside.

When nothing is found the page keeps its shape and says what was read instead of what was found: the field
turns green, one square per page, and under it one row per family of rules says how many of its rules ran and
found nothing; a family whose rules have nothing to read in this KB (D371) says so instead of claiming a pass.
A row opens *What it checks*.

*What it checks* (`GET /api/ui/v1/kbs/<kb>/checks`): how checking works in three steps (fixed rules, no AI; the
sure fixes applied by Cartographer; the rest shown under *Issues*), the result on
this KB (rules that pass, that found something, that have nothing to read here),
then one folded line per category with what its rules read and
what they found. Open, each rule is one line — its name, what it looks for, a
mark when Cartographer fixes it by itself, its count — those that found
something first; a rule that cannot run on this KB is greyed out with its reason
(D371) instead of a zero.

Like the rest of the UI the panel writes nothing. A caller that cannot see the whole KB gets the issues it can
see. The rail marks Health with a dot while there are problems. The former Observatory and
Maintenance links (`panel=observatory`, `panel=maintenance`) open Health.

The **Artifacts** panel, last in the rail, shows what the KB ships to agent
clients: skills, subagents, hooks, MCP descriptors, the
curated `instructions.md` and templates. It is a catalog page built like the
others (D339): its title counts the artifacts, the line under it gives the
artifact findings ("No artifact findings" when clean, `finding_counts`
in `GET …/artifacts`, counting those no listed artifact
owns) and says when no client has reported a use yet, and a search box filters
names and descriptions. The hero filters by kind (each with its own mark and
colour) and, when there is something to show, to the recently used, the idle
(never used, or not in the last 42 days) or those with findings. Below, each
kind is a section saying what that kind is — and, when all its artifacts go to
the same clients, how many — with one tile per artifact: name, worst finding,
two lines of description, its clients when they differ, and its last use once
any use has been reported. Bundled skills are not listed: they belong to the
binary; skills left out for failing validation are listed above the catalog.

Every kind a KB can hold gets a chip and a section, **whether or not the KB
ships one** (D864): the page is also how a reader learns what a KB can provide
to its agent clients. An empty kind is an outline tile where its first artifact would go (D948): what the kind
is, where its files go in the KB (`skills/<name>/SKILL.md`, shown as text), the clients that receive it or "Stays in the KB: no client receives it" for a
template, and a *Docs* link to the page that explains it on GitHub (the
repository's `main`, in a new tab). The kinds, their order, paths and docs come
from the server (`kinds` in `GET …/artifacts`, from `provisioning.Kinds`); the
blurb, the glyph and the colour of a kind are the page's own, and a kind it does
not know gets a neutral glyph and no blurb. A filter or a *Show* slice hides the
empty kinds, so "no match" still reads as no match, and a KB with nothing at all
shows the six empty kinds instead of a page-level empty state. Against a server
that sends no `kinds` the page behaves as before: only the kinds that have an
artifact, and the page-level empty state.

Selecting a tile opens the artifact beside the catalog, as the reading panel
opens beside the atlas, with a close button; on a narrow screen it takes the
whole width with a way back. The detail gives its description, its signature,
content hash and last use as one line of pills, the clients sync writes it to
as a sentence, and its files: Markdown rendered (frontmatter as a light table
without the name and description the header already shows, list values such
as `tools` as chips folded past eight, nested values as a folded YAML block),
anything else as plain text, a binary or over-256-KiB file named but not shown.
Its findings are listed under **Findings**. A skill, agent or hook lists the
concepts it reads, each opening on the atlas; a template offers the atlas
filtered to the concepts of its type. The selection is in the URL
(`panel=artifacts&artifact=<kind>/<name>`). The panel exists only for a
principal that can read the whole KB, since artifacts are whole-KB resources
(D238); the rail entry carries no count.

The **Secrets** panel lists the KB's `secrets/*.sops.yaml` files (D708): per
file its path, value count, last change and age recipients, then one row per
value — its JSON pointer, its type and the concepts whose `secret_refs` name it —
with the values masked. Key names are readable without a key, as they are in
git. Pasting an age identity (`AGE-SECRET-KEY-1…`) and pressing *Decrypt* opens
the values **in the browser**: the server only serves the ciphertext, never sees
the key and never runs sops. Each value can then be shown or copied; *Lock*, closing
the panel or switching KB forgets them, and the identity itself is not kept
after the decryption. A key that is not a recipient of any file is reported as
such. A KB with no secrets says where a file goes (`secrets/<name>.sops.yaml`,
from the `kind` the route sends, D864) and links the docs. Like the Artifacts
panel it exists only for a principal that can read the whole KB.

### Runtime secrets

Git credentials, MCP tokens, and the age/SOPS keys are runtime secrets — never in the bundles. The
files of the credentials directory (§Credentials directory) and `CARTOGRAPHER_TOKENS` are mounted or
configured outside committed YAML.

### Cold start

`clone`/`pull` of the KBs (including bootstrapping the remotes in `kbs:`, §Bootstrapping a KB from a git remote) + rebuilding missing indices. **Listener first, then all KBs at once** (D348): in HTTP mode `serve` binds the port before any clone or index work, so `/health` answers within a second of start. Until every KB is cloned, opened and reconciled, `/health` stays `200 status:"ok"` with `bootstrapping:true` and a `phase` (`cloning`, `mounting`, `indexing`), `/ready` is `503`, and every other path (`/mcp`, `/ui/`, …) is `503 kb bootstrapping, retry` with `Retry-After: 5`; then all KBs become available together (the routed mount registers the union of the KBs' tools, so a half-mounted server cannot be routed). On a cold `emptyDir` this window is the whole clone plus the first index build (minutes for a KB of ~16k objects); a restart that keeps the data pays only the reconcile. A failure during bootstrap (wrong remote, bad key) still ends the process, so it crash-loops visibly. stdio mode is unchanged: no listener, bootstrap runs before the transport.

The persisted SQLite index (`<kb>/.cartographer/index.db`) is excluded from git via `.git/info/exclude` (D62, never versioned): after a fresh clone (e.g. pod restart) it starts empty even if the concepts are already on disk. At startup, for every mounted KB, the server reconciles content hashes from the `.md` files against the persisted index, adding new concepts, refreshing changed ones, and removing vanished ones — keyword/FTS5 only. The same reconciliation runs after a `SyncIn` pull that moves HEAD. Best-effort: an error doesn't block startup, it is logged to stderr.

Imports and manual filesystem edits no longer require a service restart: call the MCP `reindex` tool, or run `cartographer reindex [--kb <name>] [--full]`. `--full` forwards `full: true`, rebuilding the index from every concept instead of diffing (D136). The CLI calls a healthy configured server over HTTP so it never opens that server's SQLite database; only when the server is down does it reconcile the configured local KB databases directly — there is no live in-memory index to rebuild in that case, so `--full` reports that it is reconciling instead.

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

`${NAME}` as the whole value of `token` is read from
the environment at startup; an unset or empty variable refuses startup, naming
the field (#680). Any other value is taken literally.

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

The compliance trail (D119) is always on (D905): `<data>/audit/audit.log`, or `audit.log`, signed with
`audit.key` from the credentials directory, which the first start generates when it is missing. Semantics
and the guarantees are in [transport-auth](transport-auth.md) §Operational audit; this is the
operator view.

```yaml
audit:
  log: /var/lib/cartographer/audit.jsonl # empty = <data>/audit/audit.log
  mode: required                         # best_effort (default) | required
  max_segment_bytes: 67108864
  archive_dir: /var/lib/cartographer/audit-archive
  retention_days: 400
```

The start logs where the log is and whether it is signed: `audit log: <path> (mode <mode>, signed; verify
with --public-key <hex>)`, or `unsigned` with a warning that says why.

A session started with `serve --kb <folder>` and no `data` (stdio, development and tests) keeps no audit
log and no credentials directory in the home directory: the log runs only if `audit.log` is set, and the start
logs `audit log: off` otherwise (`audit.mode: required` then does not start). Credentials are read from
`credentials_dir` when it is set. A server with `data` or `kbs[]` is unchanged.

Rollout notes:

- **A restart waits for the previous process.** Only one process writes the log; a start that finds
  its lock held retries for up to 10 seconds, so a rolling or service restart, where the old process
  is still exiting, opens the log. A holder that stays longer is a live second writer: `required`
  does not start and `best_effort` starts without a log, as for any log that cannot be opened.
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
  It is in the start's log line, and derives from `audit.key`: back the key up
  with the log, since a new one starts signing with a different key.
- **An unsigned log stays verifiable.** A log kept without a key before D905 is
  signed from the upgrade on. `audit verify` accepts its unsigned entries up to
  the first signature, which covers them through the hash chain, and up to the
  first checkpoint written with the key; an unsigned entry after either is
  reported. So a `best_effort` start without the key, once the log has been
  signed, leaves a log that `audit verify` refuses from that entry on: restore
  `audit.key` before restarting, or rotate the log and keep the old one aside.
- Verify from the operator machine, not the server: both commands read files
  and never contact a running instance, so they work during an outage.

## Observability

Cartographer emits structured operational messages and per-phase sync timing
to stderr. It does not expose Prometheus metrics or derive token/queue/search
SLIs from the audit file; the audit log is evidence for compliance review
(`cartographer audit verify|export`), not a metrics source.

**Liveness vs readiness (D84, D119, D176)**: `/health` is liveness — `status:"ok"` unconditionally (a
probe that restarted the process on `status != "ok"` must never fire from a KB-mounting issue); it
also carries a `ready: <bool>` field for callers that want both signals from one request. `/ready`
is the dedicated readiness endpoint: `200 {"ready":true}`, or `503` otherwise. Point k8s
`livenessProbe` at `/health` and `readinessProbe` at `/ready`. `/health` also carries
`bootstrapping: true` and `phase` **only while the KBs are still being cloned and indexed** (D348; absent afterwards, so the steady-state shape is unchanged) — a client that reads `bootstrapping` treats the answer as "could not ask, retry", never as "zero KBs". `/ready` during that window is `503 {"ready":false,"kbs":0,"bootstrapping":true,"phase":"…"}`; every other path answers `503 kb bootstrapping, retry` with `Retry-After: 5`. Probe recipe: `livenessProbe` on `/health`, `readinessProbe` on `/ready`, plus a `startupProbe` on `/health` sized to the worst clone-plus-reconcile (`failureThreshold` × `periodSeconds` of several minutes for a large KB) so liveness only starts once the port is up. With listen-first, `/health` answers in seconds, so the `startupProbe` is a safety net rather than a requirement. `/health` also carries
`started_at`, the RFC 3339 instant the answering process started, which is how a restart proves the
new process answers rather than the old one before it exits
([D266](decisions/D266-a-windows-restart-waits-for-the-old-process-and-proves-the-new-one.md)), and
`latest_version` **only when** the server's own update check knows of a newer release
(`update_check`, D254): absent means unknown or up to date, so the shape a probe depends on is
unchanged.

Two things make a server not ready, and both are reported by either endpoint:

- **no KB mounted** — e.g. a fresh local install with an empty data dir (§Cold start):
  `503 {"ready":false,"kbs":0}`;
- **a required-mode audit sink that is unhealthy** (D119) — the server will reject every
  required-mode call, so readiness fails *before* the next one is refused rather than after.
  The response names the affected KBs (`"degraded":["<kb>",…]`), and `/health` carries each KB's
  audit state under `kbs[].audit`, because a single boolean says something is wrong and nothing
  about where to look.

The audit gate applies on the HTTP path whatever the KB count: `serve` builds a multi-KB server even
for one mounted KB, so a single-KB HTTP deployment is gated exactly like a multi-KB one (D176). A
deployment with no audit sink, or one in `best_effort` mode, is unaffected.

**Connected clients (D129)**: `GET /clients` reports which clients have talked to this process,
with their self-reported name and version, the protocol version and era, a request count and a
last-seen timestamp, limited to the KBs the token can read (shape and caveats: `transport-auth.md` §Client roster). Unlike `/health` and
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
- **Native local upgrade** (Homebrew Cask or `install.sh update`): self-repairing since D121. Either path replaces the binary behind a stable path, but cannot replace the image of a process already running it. `install.sh update` calls `cartographer upgrade-repair` on the new binary right away. The Cask cannot: its install steps run in Homebrew's sandbox (temporary `HOME`, no network), so after `brew upgrade` the old process keeps serving until the next `cartographer sync` — the session-start hook, the scheduled timer or a manual run — performs the same replacement before syncing (D199). It acts only on a running service whose program is that same binary, on the loopback endpoint the client syncs against, reporting a different version; a failed replacement is a warning and the sync proceeds. `upgrade-repair` stays available to do it immediately. It gracefully replaces a **running** service (`SIGTERM` — on Windows the named shutdown event plus an explicit relaunch of the task, since a Scheduled Task supervises nothing, D217 — so in-flight requests drain and pending pushes flush), waits until `/health` reports `status:"ok"` **and** the installed version, then reconciles the configured providers in place with the ordinary `cartographer sync` policy. It is idempotent: if `/health` already advertises the installed version it skips the restart and only syncs. A deliberately stopped or uninstalled service stays that way, and a client pointed at a non-loopback (or different local) endpoint has its provider sync skipped rather than run against a possibly remote server. **Still needed afterwards:** restart already-open provider sessions, which is what reopens the MCP connection and reloads the repaired configuration.
- **When the repair does not complete**: exit `1` means the binary and the service are fine but provider sync is pending — the underlying error is printed and `cartographer sync` is the retry. Exit `2` means a running native service could not be replaced and verified; no provider sync was attempted, and `install.sh update` fails visibly. Investigate with `cartographer service status` and the server log, then retry `cartographer upgrade-repair`. `cartographer status` reports any residual skew and points at the same command.
- **Knowing that one exists** ([D254](decisions/D254-agents-are-told-when-cartographer-is-out-of-date.md)): on a client, the session-start hook runs `cartographer update notice`, which tells the agent — and through it the user — when a newer release exists and names the upgrade command for the channel the binary came from; `status` and `doctor` report the same from the cache. A server reports its own staleness as `latest_version` in `/health` and `kb_status`, versions only, for clients without session hooks and for remote operators; the generated instructions block tells the agent to relay a `kb_status` `latest_version` to the user once, with the upgrade command `cartographer update check` prints. Both `update notice` and the server's check re-ask GitHub, inside the 24 h TTL, when the cached latest equals the running version, so a release published just after a check is not hidden for a day ([D320](decisions/D320-upgrades-and-provisioning-tell-the-user-what-changed.md)). The Atlas adds the history behind the version, with each release's changelog and the same upgrade command, fetched by the server on demand under the same switch ([D862](decisions/D862-release-history-in-the-atlas.md)).
- **Kubernetes and remote servers**: unchanged. Bump the image tag; connected clients surface the version skew (D95) while the rollout catches up. The step ends on a **verified image**, not on a rollout ([D221](decisions/D221-the-kubernetes-upgrade-step-ends-on-a-verified-image.md)): read the Deployment's container image back and check it is the new tag *before* waiting for rollout, because until the manifest is actually applied — here by Flux on its own reconcile interval, see §CI/CD below — `kubectl rollout status` returns success immediately about the old ReplicaSet. `upgrade-repair` is for native local installations only — it is the loopback case by construction. The client-side counterpart is [D142](decisions/D142-reconnect-rebuild-a-client-configuration-never.md): each provider's lockfile entry records the `server_version` its state was materialized against, so the next `cartographer sync` prints one line saying the server changed, then syncs normally; `status` and `doctor` are the commands that recommend `cartographer reconnect` ([D220](decisions/D220-sync-states-the-server-change-without-recommending.md)). `sync` repairs in place and is the right tool for almost everything; `reconnect` (a full disconnect + connect, preserving every setting) is for what an incremental sync structurally cannot see — files and registrations an *older Cartographer version* wrote under names that are not in the current managed set. It is never automatic, and after it, already-open provider sessions still have to be restarted. The verification step that closes the loop is `cartographer doctor` ([D143](decisions/D143-doctor-a-separate-command-that-diagnoses-and-never.md)): read-only, exit 0/1/2, it reports what is left over or missing on the client — residual MCP entries for unmounted KBs, a duplicated instructions block, a hook registered twice, a v1 lockfile — and names the command that fixes each one.
- **KB format changes**: follow the release notes and back up the git remote
  before a migration. There is no general automatic schema migration engine.
- **Repo growth**: keep large binary/source archives outside the KB and link
  them; Cartographer is optimized for Markdown knowledge repositories.

## CI/CD: release + deploy pipeline

The GitHub Actions workflows (`.github/workflows/`):

- **`ci.yml`** — on every PR and push to `main`: the `test` job (`make gate`, then `make smoke-http`, `make smoke`, `make e2e` and `make test-install`; Go from `go.mod`, git identity configured for git-backed tests) `test-windows` (the tests in four shards plus `install.ps1`, on Windows runners, D785) `web` (two legs) and `docker` (the Dockerfile's `test` stage, `test/smoke/image.sh` on the built image and `make demo-smoke` on top of it, D995). `smoke-http` and `e2e` run with no git identity (they isolate their own git config), so a host without one is exercised; the Go tests get the identity above. A `test-macos` job (`macos-latest`: `test/macos/` against the real launchd, `install.sh` and the Cask GoReleaser renders, D997) runs on the release PR, on a PR touching its inputs, on `main` and on `workflow_dispatch`, not as a required check (`main-status` watches it). A `forge` job (Forgejo and GitLab CE in containers, `test/forge/run.sh`, D996) runs on the release PR, on a PR touching `test/forge/` and on `workflow_dispatch` (the workflow has that trigger), never on `main` and not as a required check. On a PR a `changes` job (`scripts/ci-filters.sh`, D994) runs `test-windows`, `web`, `demo-edge` (the demo Worker tests and Terraform check) and `docker` only when the PR touches their paths, and every job on the release-please PR (D255, D672); on `main` they always run, and a `main-status` job keeps one `ci-red` issue open while `main` is red. `test`, `test-windows`, `web`, `pr-title` and `plan-gate` are the required status checks on `main`'s ruleset (`docker` is not: the release PR and the release candidate run it, and `main-status` watches it); a job skipped by its path condition reports as passed.
- **`pr-title.yml`** — on a PR opened, edited, pushed to or reopened: the required `pr-title` check, lint of what release-please will read from the squash commit (D865). The title must be a conventional commit whose scope, when it has one, is a canonical one (`internal/changelog.CanonicalScopes()`; a test compares the list with the workflow's), and no line of the body that starts like a conventional header (`feat(mcp): …`) may be wrapped onto the next line, because release-please keeps only the first physical line of such an entry (`go run ./internal/changelog/gen -check-body`; the body reaches it through the environment, never interpolated). It is a workflow of its own, not a job of `ci.yml`, because it must re-run on `edited` and that trigger in `ci.yml` would re-run the whole suite on every PR edit. The ruleset requires the job name, not the workflow.
- **`privacy.yml`** — on every push, PR, issue and comment: scans the published commits and text for private names, held only as HMAC digests in the `PRIVACY_GUARD_DIGESTS`/`PRIVACY_GUARD_KEY` secrets; a hit fails the run and redacts the text (D259). Skipped on forks; issue and comment text is scanned only when its author or editor has write access.
- **`release-please.yml`** — on every push to `main`: maintains the **release PR** (semver bump computed from conventional commits + `CHANGELOG.md`). On merge, it creates the `vX.Y.Z` tag and GitHub Release with the notes (the merged PR body between its `---` lines). Its `release-pr` job runs after it on every push to `main`, on the open release PR's branch: it regenerates `docs/decisions.md` (D328) and groups the newest `CHANGELOG.md` section by area (`make changelog-areas`, `internal/changelog`, D865) — one `### <Area>` heading per area with its Features and Bug Fixes, the scope prefix dropped, `dev`/`demo`/`deploy`/`ci` entries left out — then pushes one commit for both files when either changed and rewrites the PR body with the same grouping when it differs, so the release page reads like the changelog. Both steps are idempotent because release-please rewrites its branch and body on every push. Uses the `RELEASE_PLEASE_TOKEN` PAT (PRs opened with the default `GITHUB_TOKEN` do not trigger `pull_request` workflows).
- **`plan-gate.yml`** — on a PR (`pull_request_target`, so the workflow and `pr-triage.sh` come from the base branch) and when a plan is labelled or assigned, an issue changes milestone or a milestone is created, edited, closed or deleted (these re-run the check): the required `plan-gate` check, `pr-triage.sh --gate`, the approved-plan and size rule of `CONTRIBUTING.md` §Pull requests (D955) and the next-train rule of §Releases.
- **`release-train.yml`** — the weekly release train (D956), `gh` only and no checkout. Two cron entries per slot, one per UTC offset of Europe/Rome, and a run continues only when the entry that fired is the one for today's Rome offset. **Saturday 00:30**: dispatches `preview.yml` with `v<next>-rc.<YYYYMMDD>` (`<next>` read from the release PR title) and comments the tag on the release PR. **Monday 08:30**: merges the release PR (`gh pr merge --squash --match-head-commit`, with `RELEASE_PLEASE_TOKEN`: a merge by `GITHUB_TOKEN` triggers no `push` workflow, so release-please would never tag) only if the PR comes from this repository, the newest successful release candidate build is of the head of `main`, `rc-ok` was added after that build by a maintainer, no `feat`, `perf`, `refactor` or breaking commit landed on `main` since Saturday 00:00, and every required check passed; otherwise it comments why and the next train is a week later. `workflow_dispatch` with `action=rc|release` runs one step by hand; `internal/repodocs/release_train_test.go` pins the crons, the guard and the merge token. The calendar is `CONTRIBUTING.md` §Releases.
- **`release.yml`** — on the `v*` tag: **GoReleaser** (`.goreleaser.yaml`) builds the `darwin/linux/windows × amd64/arm64` binaries (`-X main.version=v<semver>`, read by `cartographer version`), attaches them to the existing release with `sha256sums.txt` (`release.mode: keep-existing` — the release and its notes belong to release-please), publishes the Homebrew cask on `BeppeTemp/homebrew-tap` (`HOMEBREW_TAP_TOKEN` PAT, macOS only); in parallel, buildx publishes the multi-arch image `ghcr.io/beppetemp/cartographer:<tag>` + `:latest`. After the image push, the `mcp-registry` job publishes the server's metadata to the official **MCP Registry** (`io.github.beppetemp/cartographer`, OCI package pointing at the ghcr image): `mcp-publisher login github-oidc` (no long-lived secret) + `publish` with `server.json` (repo root; `version` and image tag are stamped by the job). Ownership is verified via the `io.modelcontextprotocol.server.name` LABEL baked into the image. A final `prerelease-flag` job marks a `v0.*` release as a GitHub **pre-release** and reads the flag back, failing the release if it is not set (D82, D211): the `"prerelease": true` key in `release-please-config.json` does **not** set it — this job does. Because a pre-release is invisible to the `releases/latest` API endpoint, `install.sh` resolves the newest tag through `/releases?per_page=1` instead; until 1.0.0, nothing should reach for `releases/latest`.
- **`preview.yml`** — dispatched by hand with a pre-release tag (`vX.Y.Z-<suffix>`, anything else is refused): builds the same multi-arch image as `release.yml` and pushes **only** `ghcr.io/beppetemp/cartographer:<tag>` — no `latest`, release, cask or MCP Registry entry — so an unreleased `main` can run on a real deployment before the release PR is merged (D303). Before the login and the push it builds the amd64 image, loads it and runs `test/smoke/image.sh` on it (D995): a red smoke fails the run, so ghcr gets no tag and `release-train.yml` names the failed build in its skip comment. `gh workflow run preview.yml -f tag=v0.18.0-rc.1`; a macOS client for it is built from source (`make build VERSION=<tag>`).
- **`docs.yml`** — on a push to `main` that touches `docs/` or `mkdocs.yml` (and by hand): builds the site with MkDocs Material (`--strict`, so a broken link fails it) and deploys it to GitHub Pages; on a PR it only builds. Only the deploy job holds the `pages` concurrency group.
- **`demo.yml`** — called by `release.yml` after the image push (and runnable by hand for a given tag): builds `deploy/demo/Dockerfile` on that release's image and deploys it to the hosted MCP demo on Cloud Run through Workload Identity Federation (D751). Skipped while `vars.GCP_WIF_PROVIDER` is empty; `mcp-registry` does not wait for it.
- **`hero.yml`** — called by `release.yml` as the `hero` job (and runnable by hand for a given tag, which always records): when the tag's `web/` tree differs from the one the README's hero was recorded from (`web/scripts/hero-needed.sh`), it runs `make hero` on the runner (headless, no GPU, deterministic, D790) and force-pushes the result as the single commit of the orphan branch `readme-assets` (`web/scripts/publish-hero.sh`), where the README reads it from `raw.githubusercontent.com`. `contents: write` on `GITHUB_TOKEN` is all it holds, it never pushes to `main`, and nothing waits on it: a failed recording never blocks a release.
- **`dependabot-automerge.yml`** — on Dependabot PRs: enables auto-merge for patch and minor rounds (D329).

Dependency updates (Go modules and action versions) come from **Dependabot** (`.github/dependabot.yml`, weekly, grouped PRs). Patch and minor rounds merge themselves through GitHub auto-merge once the required checks pass (`dependabot-automerge.yml`, D329); a major bump waits for a maintainer. The frontend's npm dependencies are not on Dependabot's version rounds: `web/package-lock.json` is part of the bundle's provenance hash, so a lockfile bump without `make web` fails `test`. A security alert on them is fixed by hand — update the package in `web/`, then `make web` and commit both.

**Deployment** to the maintainer's infrastructure happens outside the pipeline: the cluster is GitOps, and the image tag bump in the manifests repo is reconciled by Flux (D68). CI does not run `kubectl apply`: it would be redundant, would conflict with Flux's drift control, and would require a long-lived kubeconfig in the repo's secrets.

**The Windows assets** ([D252](decisions/D252-windows-installs-through-install-ps1-not-winget.md)). The darwin and linux assets stay raw per-platform binaries, because that is what `install.sh` downloads and verifies against its line in `sha256sums.txt`; the two Windows assets are **zips** (`cartographer-windows-{amd64,arm64}.zip`, each containing `cartographer.exe`), which is what `install.ps1` downloads, verifies and extracts. Renaming them breaks every existing Windows `install.ps1 update`, the same way a rename breaks `install.sh`; `test/install/goreleaser_guard.sh` pins the shape. There is no package-manager submission for Windows: nothing in the release depends on a third-party review.

Secrets required in the GitHub repo: `RELEASE_PLEASE_TOKEN`, `HOMEBREW_TAP_TOKEN`. `GITHUB_TOKEN` is provided automatically by Actions (for ghcr.io the `packages: write` permission declared in the workflow is enough).

The end-to-end operator procedure (release, rollout verification, local client update) lives outside the repo, in the maintainer's local tooling: it contains details of the private deployment infrastructure.

### Client installation (`install.sh`, `install.ps1`)

One channel per platform: `brew install beppetemp/tap/cartographer` on macOS (cask from the tap), `install.ps1` (repo root) on Windows, and `install.sh` (repo root) on Linux or on a macOS without Homebrew. The script downloads the latest `cartographer` binary from the most recent GitHub Release for the current platform:

```bash
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
curl -fsSL .../install.sh | sh -s -- update      # updates if not already on the latest release
curl -fsSL .../install.sh | sh -s -- uninstall
```

No package-manager dependency: pure POSIX `sh`, verifies the checksum via `sha256sums.txt` if present in the release, installs into `/usr/local/bin` (falling back to `~/.local/bin` if not writable, overridable with `CARTOGRAPHER_INSTALL_DIR`). See `docs/configurator.md` §Installation.

**`install.sh` is Darwin and Linux only, and says so where it matters.** Run from Git Bash, MSYS2 or Cygwin — where `uname -s` reports `MINGW64_NT-…`, `MSYS_NT-…` or `CYGWIN_NT-…` — it refuses with the `install.ps1` command instead of a generic "unsupported OS", because that string is the whole remedy.

**`install.ps1` is the Windows counterpart, with the same contract** (D252): `irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex` installs; `update` and `uninstall [-BinaryOnly]` are passed through `& ([scriptblock]::Create((irm …/install.ps1))) <command>`. It resolves the newest tag through `/releases?per_page=1` like `install.sh`, downloads `cartographer-windows-<arch>.zip` for the **OS** architecture (an x64 PowerShell on ARM64 still gets the native build), and refuses on a missing `sha256sums.txt`, a missing line for the asset, or a digest mismatch — stricter than `install.sh`, which still accepts pre-checksum tags that carry no Windows assets anyway. It installs into `%LOCALAPPDATA%\Cartographer\bin` (`CARTOGRAPHER_INSTALL_DIR` overrides) and adds that directory to the **user** `PATH` and to the current process's, so under `iex` the command works on the next line. Windows refuses to overwrite a running executable but allows renaming it: an update moves the old `cartographer.exe` aside as `cartographer.exe.old`, drops the new one in, runs `upgrade-repair` with the same exit-code handling as `install.sh`, and deletes the leftovers once nothing holds them — so no `service stop` is needed around it. `uninstall` refuses while `%LOCALAPPDATA%\cartographer\tasks\serve.xml` or `sync.xml` exists, naming the teardown, and removes the `PATH` entry with the binary. The script never calls `exit` (under `iex` that would close the user's window) and sets its preference variables only inside functions, so the caller's session is left as found. It is exercised end to end by the `test-windows (install.ps1)` CI leg, under PowerShell 7 and Windows PowerShell 5.1, by `test/install/windows/run.ps1`.

After replacing the binary the script runs `cartographer upgrade-repair` (D121, §Upgrades, schema migration, and repo growth) instead of a bare `service restart`: a running service is gracefully replaced and version-verified, a stopped or absent one is left alone, and the configured providers are re-synchronized. A pending provider sync (exit `1`) is logged without failing the update; an unverifiable running service (exit `2`) fails the `update` command **after** the new binary is in place. The generated Homebrew Cask runs the same command from its post-install hook, non-fatally: its source of truth is `.goreleaser.yaml` in this repository, never a hand edit in the tap.


> The repo is public on GitHub: a plain `curl` works with no authentication. The script supports an optional `GITHUB_TOKEN` to avoid API rate limits on frequent runs (e.g. CI).
