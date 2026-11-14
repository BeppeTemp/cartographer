# Compass: the multi-provider client

The `cartographer` binary bundles, besides Meridian (`serve`), the Compass subcommands that
connect a machine to a Cartographer server: `agents` (discovery), `connect`
(configures + materializes provisioning artifacts — skills, agents, hooks, instructions,
mcp — D69), `disconnect` (disconnects, the inverse of `connect`), `status` (drift, with per-kind
counts), `sync` (realigns), `resolve` (resolves a `{{repo:}}`/`{{path:}}` placeholder, D75). Invoked
with no arguments in a terminal it prints a short status, then the usage.

Compass **always talks to Meridian over HTTP** (the `sync_pull` tool): there is no
stdio transport on the client side, nor a separate binary — see
[the Compass decision records](decisions.md#client-configurator) for the
rationale. Generating the MCP config files (`internal/configurator`) and the
materialization logic (`internal/provisioning` for the apply half, `internal/provisioning/contract` for the manifest) are the same used server-side by
`sync_check`/`sync_pull` (`docs/sync.md`).

## Subcommands

### Discovery, version and output formats

Root help groups commands into Get started (`help`, `setup`, `version`, `update`, `docs`), Compass, Meridian, Knowledge base and
Diagnostics. `cartographer version` and `cartographer --version` are aliases;
the help of root, `paths`, `kb`, `client` and `workspace` (`help`, `-h` or
`--help` as the only argument) and of `service` (`-h` or `--help` only) writes to
stdout and exits 0, with no client config needed; `-h`/`--help` anywhere in the arguments of
`agents`, `status`, `approve`, `config`, `mcp`, `update`, `client`, `workspace` and
`service sync-timer` does the same, and no subcommand answers help with an `Error:` line or exit 2; no argument outside a terminal
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
errors. For each connected provider whose managed instructions block is over 16 KiB (about 4k
tokens, bytes/4, approximate; [D1182](decisions/D1182-the-managed-block-is-brief-by-default.md)) `status`
prints `[<provider>] instructions block <size> KiB (~<n>k tokens, approximate) over 16 KiB: <the three
largest KBs>` and `sync` prints the same line after its report; JSON carries it additively as
`instructions_blocks` (path, bytes, approx tokens, threshold, over, largest KBs) on each provider, schema
unchanged. It informs and changes neither the state nor the exit code, and a block that was not written
is not measured. `service status` retains 0 running, 3 stopped (on every platform, a Windows task left `Disabled` or `Ready` by `stop` included) and 4 not installed.

### `cartographer hook write-findings [--channel stderr|context|copilot]` (internal)

Not in the usage text. The logic of the client-generated `cartographer-write-findings` hook
([D353](decisions/D353-write-findings-hook.md), `docs/sync.md` §Layer 1): reads a Claude Code, Codex, Copilot (CLI, VS Code), Pi or OpenCode 2.x
`PostToolUse` payload on stdin and looks in `tool_response` (the result object, a content-block
array, or JSON in a `text` block, or a string of several JSON values as OpenCode joins them; `supersede` appends `findings:` as plain text) for a `findings`
array or `results[].findings` (`concept_batch`). With at least one finding it writes a fixed English
message (a header with the count, up to 10 `<check> <path>: <message>` lines, `+N more`, and the
instruction to fix them: nothing accepts a finding, D907; a finding wrong for a page that is right as written is a check defect, reported in the session log and never closed by removing evidence, as kb-doctor step 6 says). `--channel stderr` (default, Claude Code) writes it to stderr
and exits 2; `--channel context` (Codex, [D361](decisions/D361-codex-write-findings-context.md))
prints `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"<message>"}}` on
stdout and exits 0, never 2; `--channel copilot` (Copilot CLI, D676) prints
`{"additionalContext":"<message>"}` on stdout and exits 0, reading Copilot's camelCase payload
(`toolName`, `toolResult.textResultForLlm`, the tool named `<server>-<tool>`). Anything else (no findings, an unknown shape, a parse error, a panic,
an unknown flag or channel) is silent exit 0; with the stderr channel stdout is always empty. A wrong
subcommand name exits 2. `tool_name` may be `mcp__<server>__<tool>`, `<server>_<tool>` (OpenCode, [D362](decisions/D362-opencode-write-findings-plugin.md)), `<server>-<tool>` (Copilot) or the bare tool name.

### `cartographer hook dispatch --provider cline --event <Event>` (internal)

Not in the usage text. The logic behind the dispatcher files Cartographer will own in Cline's hooks folder: reads
Cline's hook payload on stdin (`taskId`, `workspaceRoots`, `preToolUse`, `postToolUse`, `userPromptSubmit`) and runs the
Cartographer hooks of the event found under `~/.cline/cartographer-hooks/*/hook.json`. `TaskStart` runs the `SessionStart`
hooks, once per task (a 12 h marker in a private per-user directory of the temp dir); a tool named `<server>__<tool>` reaches them as
`mcp__<server>__<tool>`; a `matcher` applies to `PreToolUse` and `PostToolUse` only. It prints one line of ASCII JSON with the three fields Cline reads, `cancel`, `contextModification`
and `errorMessage` (`{}` when there is nothing to say): `PreToolUse` exit 2 sets `cancel` (Cline stops the whole task),
`PostToolUse` exit 2 with stderr and `SessionStart`/`UserPromptSubmit` stdout become `contextModification`. It always exits 0:
an unknown provider, event or flag, a bad payload and a panic are all `{}`. `docs/harnesses.md` §cline has the contract.

### `cartographer update check|notice`

Tells an agent, and its user, that a newer release exists
([D254](decisions/D254-agents-are-told-when-cartographer-is-out-of-date.md)). The source is the
GitHub release list (`/releases?per_page=100`, up to three pages, which includes the pre-releases every 0.x tag is;
the newest one that is not a draft and has a version tag without a pre-release suffix wins: a
release candidate `v<next>-rc.<YYYYMMDD>` is never offered, whatever its GitHub flag, D1192),
cached for 24 h in `<user cache dir>/cartographer/update-check.json`. A lookup times out after 3 s
and any failure means "unknown": nothing is printed. A `dev` build never checks.
A newer release is announced only once the channel the binary came from can install it
([D1351](decisions/D1351-update-check-waits-for-the-channel.md)): `homebrew` when the tap's cask
names that version, `install.sh` / `install.ps1` when the release carries this platform's binary and
`sha256sums.txt`; `go-install` and unknown ask for nothing more. Until then nothing is printed and
no release is cached; the not-yet-installable tag is remembered for an hour (`update-check.json.pending`), so session starts do not re-ask, while `update check` always asks and prints `latest vX (not yet installable through <channel>)`. A current binary makes no extra request.
`CARTOGRAPHER_NO_UPDATE_CHECK=1` or `update_check: false` (below) turns it off;
`CARTOGRAPHER_UPDATE_API_URL` and `CARTOGRAPHER_UPDATE_CASK_URL` exist for tests only.

| Command | Effect |
|---|---|
| `update check [--output table\|json]` | Refreshes regardless of the cache age, prints current, latest, channel and command. Always exit 0 (2 on usage errors). JSON: `{current, latest, available, kind, channel, command}`, additive |
| `update notice [--channel text\|antigravity]` | What the session-start hook runs. Prints **nothing** unless an update exists, then one paragraph for the agent: the versions, the upgrade command, and the instruction to tell the user once and run it only on consent. `--channel antigravity` outputs protojson (`injectSteps`, or `{}` when up to date). Always exit 0 |

The command depends on the channel the running binary came from: `homebrew` (Caskroom, or the
`/opt/homebrew/bin`/`/usr/local/bin` symlink into it) → `brew upgrade --cask beppetemp/tap/cartographer`;
`install.sh` (`/usr/local/bin` or `~/.local/bin` as a plain file, or `CARTOGRAPHER_INSTALL_DIR`) →
`curl … install.sh | sh -s -- update`; `install.ps1` (`%LOCALAPPDATA%\Cartographer\bin`) → the
`install.ps1 … update` one-liner; `go-install` (`GOBIN`/`GOPATH/bin`) →
`go install …@<latest>`. `container` (`/.dockerenv`) and `unknown` get no command: the notice
names the image tag or the releases page.

Compass **never installs a release by itself** (D906): there is no auto-patch, only this notice and the
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
  `kb create <name> --remote <url>`; refs on exactly one branch are fetched shallowly into a temporary directory and, when that branch is only a forge's initial commit (`gitx.ForgeInitialized`), planned as create too, the plan line saying the commit is adopted (D710); any other ref means content: `kb clone <url> <name>` — which, for a repository holding only a forge's initial commit (README/LICENSE/.gitignore), answers with `kb create <name> --remote <url>` instead, since that adopts it (D710). A README.md of that commit is replaced by a short generated one (a new commit on top, the forge's commit stays) only when it is a known forge boilerplate (the title and at most a description line of GitHub/Gitea/Forgejo, GitLab's "Getting started" template); any other README is kept and a hint names `--readme generate`, which replaces it whatever it holds, while `--readme keep` never replaces it (D1071). `kb create --branch X` (with `--remote`) makes `X` the KB's branch: it writes `branch: X` in `settings.yaml`, pushes the default branch carrying it, then creates and pushes `X` and leaves the clone on it; `kb clone --branch X` is per server instead: it checks `X` out and adds a `kbs[]` entry with `branch: X` to the server config (D1093). On a repository that already has history, `kb create --branch X` (X absent on the remote) creates the KB as an orphan branch `X` with no shared history, pushes only `X` (a plain push, the default branch is never touched), leaves the clone on it and records `kbs[].branch: X` in the server config, which must exist; an existing `X` is refused, and without `--branch` the refusal names it as a way out (D1412). `kb clone --branch X` mounts it even though the default branch holds no KB, and `kb clone` without `--branch` on such a repository lists the branches that do (D1412); `setup`/`connect` route any remote with refs to `kb clone` and have no `--branch`. The name is
  `--name`, else the repository name. The same probe proves the remote is reachable with this
  machine's credentials, under the non-interactive git environment of `kb clone` (no prompts,
  SSH batch mode, host keys never auto-accepted — D173); a failure exits 2 **before anything is
  written**, with the remedy for the recognised git errors. A second probe, `git push --dry-run` of a
  throw-away commit to a branch that does not exist (`gitx.ProbePushAccess`), asks whether those credentials may
  *push*: a read-only token or deploy key exits 2 at once with the same remedy, instead of failing at the first write. Only a real refusal (the verdicts `kb clone` and `doctor` share) stops setup: a remote that stopped answering, a deadline or a failure that says nothing about the credential is a `Note:` on stderr and setup goes on, and Ctrl-C prints `push check interrupted — nothing was changed` and exits 1, like setup's other cancellations. The same probe runs after `kb clone` and in `doctor` (below), with the server's credentials for the KB (its `<kb>.token`/`<kb>.ssh`, else `default.*`, in the credentials directory), not only the machine's; the rest of the environment (`GIT_SSH_COMMAND`, `SSH_AUTH_SOCK`, proxies) is the one of the shell running `kb clone` or `doctor`, not the service's.
  It sees access only; a repository rule or required signature shows at the first real push.
- **The KB's initial commit is authored by the operator (D904, D156).** `kb create` (so `setup`'s KB
  step) creates the repository first and then asks git who the operator is there, so a global identity
  and an `includeIf gitdir:` for the data dir both apply; the author is also the committer. Only a
  machine with no identity gets Meridian's `Cartographer <cartographer@host>`, and with `--remote`
  the command warns before committing (it does not stop), naming that author and both routes: a global
  `user.name`/`user.email`, or an identity for the KBs only, which takes the same `Name <email>` in three
  places: an `includeIf "gitdir/i:<data dir>/"` in the global git config, `git.identities.default` in Meridian
  file, `knowledge_bases.default.identity` in `.cartographer.yaml`. A first push the remote refuses is classified like
  Meridian's sync does: a refused author names that author and the identity to set (with Meridian
  identity, `commit --amend --reset-author --no-edit` re-authors the kept scaffold), a protected branch
  says so, and only otherwise the hint is about authentication.
- **Workspace.** `--workspace <repo>` is passed through to `connect --workspace` (D193): the KBs are
  confined to that repository instead of every directory.
- **Plugins.** `--plugin <source>` (repeatable; a git URL, optionally `@<ref>`, or a folder) installs a plugin
  before the agents are detected, and `--approve-plugins` approves the ones installed by this run after printing
  what each runs; without it nothing is approved ([plugins.md §Onboarding](plugins.md#onboarding-setup-and-connect)).
  A plugin may bring the agent client `setup` then connects.
- **KB binding (D190).** `--kb` is passed through. Without it: one KB binds itself; agents that
  already carry a binding keep it; a KB this run adds next to existing ones is bound alone (the
  narrowest choice); otherwise, with two or more KBs and nothing to go on, an interactive run
  asks with the same picker as `connect` and a non-interactive one exits 2 naming `--kb`.
- **Refusals, all before any change:** no `git` on `PATH` (2.43 or later is required, D2038); a client already pointed at a
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
- **Bindings and the server URL.** A KB this run adds is bound only to the agents that have no binding yet
  (two `connect` runs: the unbound ones get `--kb <new>`, the bound ones are connected without `--kb`, so
  `connect` does not reset what they have); the plan names what each agent receives. When Compass has no
  URL yet setup passes `--server-url` for the local service's own address (a service installed with
  `--http` on another port included); a loopback client URL on a different port than the service is a
  plan-time refusal (exit 2, nothing changed).
- **Service backend (D1682).** The plan line names how Meridian is kept: `systemd`, `launchd` and
  the Scheduled Task say "starts at login, no admin rights"; `cron` says "starts at boot, checked every
  5 minutes". A server already answering a well-formed `/health` on the configured address (the default
  one when there is no config yet) is **kept** even with no installed service ("already answering ...
  not managed by Cartographer"); anything else on the port is not one of ours and falls through to the
  install, which fails at bind with Meridian's own message. With no systemd user manager, no usable
  `crontab` and no answering server, the plan fails: exit 2, nothing created, the message gives the
  manual route (`service install --config-only`, then `serve --config ... &` hooked into the init, then
  `setup` again). A kept hand-started server cannot be restarted by setup: after a KB create or clone
  setup stops with exit 1 asking for a restart by hand. The failure hint names only commands that
  work on the backend (the cron log paths, not `journalctl`).
- **Readiness.** Before the agents step setup waits (up to 5 minutes, a first clone of a large KB takes that
  long) until `/health` stops reporting `bootstrapping` — a server answers 200 while it clones — and the
  verify step uses the same condition, so "set up" is never printed over a server still loading its KBs.
- **Interactive** (a TTY, no `--no-input`): asks for the remote when none is mounted — blank means
  a local-only KB, confirmed explicitly (D134) — then which of the detected agents to connect, prints
  the plan and asks `Proceed? [Y/n]` (skipped by `--yes`). `--dry-run` prints the plan and exits 0.
- **Exit codes:** 0 set up; 3 every step ran but an agent is partially or not connected because a destination is a symlink (`connect`'s exit 3: setup names the agents and what was skipped instead of "Cartographer is set up."); 1 a step failed (whatever code the step itself returned: the service and the KB may exist by then) or the operator cancelled; 2 usage, preflight or
  choice error (nothing changed). A failed step prints `Setup stopped at …`; a rerun skips what is
  done. On Linux a freshly installed user unit without lingering gets the `loginctl enable-linger`
  hint (systemd only: cron has no logout problem). The run ends on the Atlas URL and on restarting the agent sessions.

```bash
cartographer setup                                                  # interview, plan, confirm
cartographer setup --remote git@github.com:me/wiki.git --agents claude --yes --no-input
cartographer setup --no-remote --name trial --agents codex --yes    # local-only KB
cartographer setup --remote <url> --dry-run                          # the plan, nothing changed
```

The install scripts end a first install on `Next: cartographer setup`. The Homebrew cask cannot
tell a first install from an upgrade, and its caveat names both: `First install: cartographer
setup`, and for an upgrade that the next `cartographer sync` switches the service to the new binary
or `cartographer upgrade-repair` does it now (D280). Bare `cartographer` prints the same hint on its `next` line while a loopback server has no service
behind it or answers with no KB mounted.

### `cartographer agents`

Lists the supported providers, whether they are installed on the machine and whether they are
connected (recorded in the Compass state file `.cartographer-state.json`, beside the machine-wide `~/.cartographer.yaml`).

`internal/agents.Detect` probes, in this order, and stops at the first match; the heuristic that
matched is reported in the DETECTION column, and as `detected_by` in JSON
([D224](decisions/D224-detection-records-which-heuristic-matched.md)):

1. `binary` — any of the provider's binaries in `PATH` (Kiro ships as `kiro` from the IDE and `kiro-cli`
   standalone). On Windows `exec.LookPath` honours `PATHEXT`, so this is where a client installed
   normally is found;
2. `config-dir` — a known configuration directory under the home directory (`~/.copilot` is also what
   detects VS Code's Copilot harness, which creates it on first use; no VS Code directory is probed,
   [D1864](decisions/D1864-copilot-vscode-client.md));
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
fork-cli (plugin team, base crush)  yes  no  binary  /usr/local/bin/fork-cli
```

A provider a plugin declares (`providers/<id>.yaml`, [plugins.md §Providers](plugins.md#providers)) is listed after
the built-ins, its PROVIDER cell labelled `<id> (plugin <name>, base <base>)`. It is probed as its base is (a binary
is looked up under the base's name) and one whose plugin is not active is never reported installed.
A cell the resolver left out (a symlink not listed under `providers.<provider>.allow_symlinks`, a path outside its root) is not silent: `sync` and `connect` print `[<provider>] warning: cell left out: <reason>` once per run, `status` prints it under the provider's row, and `doctor` reports a `plugin-provider` warning ([plugins.md §Resolving](plugins.md#resolving)).

The distinction the column carries is the one `INSTALLED` cannot: `binary` is a client that is
on the machine now, while `config-dir`, `env-config-dir` and `app-dir` are
directories, which a client that was removed leaves behind. `INSTALLED` — and therefore which
providers a bare `connect` or `connect all` targets — is unchanged: it is still true for any
match ([#305](https://github.com/BeppeTemp/cartographer/issues/305)).

### `cartographer connect [provider|all]`

To a local server without authentication, `connect` also writes the user's git identity (`git config user.name`/`user.email`)
into `git.identities.default` of the server file when it has none, and prints the missing identity when there is no real
one (`status` prints it too): see `deployment.md` (D1974).

**Windows with WSL** (`deployment.md` §Windows with WSL). Each side's `connect` provisions only its own home: the Windows binary `%USERPROFILE%`, the WSL binary the distro's `$HOME`. On Windows, after a connect to a loopback server that answered, with no WSL marker and no native `\Cartographer\Serve` task, the running distro whose `cartographer service status` exits 0 is recorded in the marker (`server: WSL distro <d>`); only running distros are asked, a machine without WSL, one with no running distro and one with a marker already set hear nothing, and any other exit that records nothing prints one stderr line `note: WSL server owner not recorded: <reason>` (native Serve task registered; `<distro>: service status failed (<error>)`; marker not written; wsl listing failed). When the loopback server does not answer and a distro has cartographer, the native install is not offered: the hint is `cartographer service install --wsl <d>`. With the marker set, an explicit `http://[::1]:<port>` URL is refused (exit 2): WSL forwards `127.0.0.1`, not `[::1]`. In WSL with Windows interop, a connect ends with one line pointing the Windows agents at `cartographer connect` in PowerShell; it never runs the Windows binary.

Generates the MCP config (HTTP transport only), materializes artifacts via `sync_pull` (the full
kind×provider matrix is in `sync.md` §Kind × provider matrix; combinations with no known
destination go to `unsupported` and are filtered upstream), and writes/updates `.cartographer.yaml`. If the
server is unreachable, the MCP configs and `.cartographer.yaml` are still written; materialization
is **deferred** (warning, exit 0) — it must be completed with `cartographer sync` once the
server is up. A transport failure (dial, DNS, timeout) defers as "server unreachable"; any other answer that is not a rejection (a 404 or 5xx from `/health`) also defers, worded "Meridian did not serve the skills", and probe messages say "Meridian answered but the probe failed" for it; a server that answers 401/403 fails `connect` (exit 2) naming the rejected token and where it came from, and a signature error fails it naming the KB, in both cases without saving `.cartographer.yaml` (#1299). A provider whose destination the symlink guard refuses ends `partially connected` or `not connected` and `connect` exits 3 ([§Symlinked config files and directories are refused unless listed](#symlinked-config-files-and-directories-are-refused-unless-listed)). Materialized hooks are also **automatically registered** in the
provider's native mechanism (`settings.json` / `config.toml` / JS plugin — `sync.md` §Agents and
hooks); `connect`/`sync` print an info line for each one. `connect`, `sync` and `upgrade-repair` also remove the
scheduled headless doctor job an earlier release installed, printing one line when there was one:
Cartographer never starts an agent by itself (D704), except a Surveyor the operator has configured and deployed (D1258, [the Surveyor](deployment.md#the-surveyor-a-third-role)). `sync` matters after a Homebrew upgrade, which
runs neither of the others; `doctor run|schedule|unschedule|status` fail as unknown arguments.

**Token file (D699).** With `server.auth: true` and a `server.token: ${NAME}` reference (the default is `${CARTOGRAPHER_TOKENS}`),
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

**Where the token goes.** Every client request that carries the bearer token (MCP calls, the usage, settings and heartbeat reports, `reindex`, `kb repair`, `kb secrets`, the Surveyor's ping, the stdio bridge's forwards) is preceded, once per client, by an anonymous `GET /health` that must answer as a Cartographer server (`status` `ok` and a `version`; a bootstrapping server counts); otherwise the request is not sent and fails with "does not answer as a Cartographer server". The check sits in the client where the `Authorization` header is set (`MCPClient`, `Bridge.authorize`), so a new caller cannot skip it. It is not authentication: any listener can forge that shape. The trust in the destination is the configured `server_url`; the check only stops the token going to a program that merely holds the port.

**Multi-KB servers (D92).** `connect` reads `GET /health` before emitting MCP
configuration: anonymously first, then with the token once that answer is a Cartographer server's (`version` present), never before, and with no redirect followed (a scoped token sees only the KBs it reads, D1096; a listener that is not a server, or a proxy that refuses the anonymous `/health`, never receives the token). With one mounted KB (or an older single-KB server that omits
`kbs`) it keeps the compatible single entry, `<server_name>`, pointed at the
bare `/mcp` URL. With two or more KBs it writes one entry per KB, named
`<server_name>-<kb>` and pointed at `/mcp?kb=<kb>`, and records that KB list in
`.cartographer-state.json` (`known_kbs`). `sync` repeats the enumeration: it adds new
entries, removes entries for disappeared KBs, and performs the bare↔suffixed
rename on one-to-many transitions. If Meridian cannot be reached, it leaves
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

**Read-only profile (D1184).** `connect <provider> --access read-min` writes that provider's entry as `<server>/mcp/routed[?kbs=…]&access=read-min&provisioned=1`: the connection is read-only and lists a minimal tool set instead of the full one, for an agent that only reads (`control-plane.md` §MCP API). The value is saved in `providers.<provider>.access` with `providers.<provider>.tools: minimal` for the providers of that run, which is not a KB binding (the provider keeps receiving the KBs it received), and a later bare `connect` or `sync` keeps it; clearing it is editing the keys out of `.cartographer.yaml`, then `sync`. Two refusals name the fix: a server with no routed endpoint (or that did not answer), and a provider with a workspace binding, whose bridge does not carry the profile (`workspace unbind` it first). A provider that has the profile never gets the bridge: `workspace bind` refuses it, and bindings it gains otherwise (derived ones) are ignored, with a warning from `sync`, while it keeps its HTTP entry. The interactive form does not offer it.

**Workspace scope (D193).** `cartographer workspace bind <provider> <path> --kb <name>…` moves a
provider from one machine-wide catalogue to one projection per bound repository: the KBs land in
that repository's own project-local directories and nothing KB-sourced is written under `$HOME`. `connect --workspace <repo>` makes the same choice at connect time, which is the only moment
it is free — a provider-global connect materializes every selected KB into `$HOME` first, and moving
them afterwards is a migration. `workspace unbind` removes the declaration and the next `sync`
prunes what was projected there (`workspace unbind <provider> --all` removes every explicit binding of the provider and returns it to the global scope even when KB-derived bindings exist; a single-path unbind of the last explicit binding keeps the scope while derived ones remain and prints a note); a path never bound for that provider is an error (exit 1). `workspace bind` refuses a provider that is not connected (`<provider> is not connected`, exit 2, as `client bind` does), dedupes `--kb` and,
like `client bind`, refuses a KB Meridian does not mount when Meridian answers with its list (exit 2); with Meridian unreachable it warns and binds. For Codex, `workspace bind codex <path> --kb <name>… --trust` also records the
folder as trusted in `~/.codex/config.toml`, so the projection is not born `inactive` (one explicit, narrow
write; without the flag `bind` prints the line to add; `unbind` and a derived binding never touch it; refusal
cases and the exact rules in `sync.md` §Workspace scope, [D1074](decisions/D1074-workspace-bind-codex-trust.md)). Binding `$HOME` itself is allowed, but warns on stderr that everything the KB distributes then reaches every session of the provider (exit 0, binding saved). A binding also scopes the **tools** in every client, with no file
written into the workspace: `sync` writes the provider's global MCP entry as a local stdio bridge,
`cartographer mcp --provider <p> [--dir <folder>]` (the folder defaults to the working directory), which serves only the KBs of the folder the session runs in
(deepest binding wins; an unbound folder, `$HOME` included, gets the provider's default set
**read-only** and no KB artifact (D1018), or read-write when `unbound_access` (`defaults.unbound_access` or the provider's own) says `write` (#1356, still no artifact); `--no-kb` means no KB, never every KB). `knowledge_bases.<kb>.access: read` holds in every folder: the bridge carries it (D1409). It is a scope filter, not a security boundary: hard isolation needs a per-workspace
token (D44/D45). Artifacts stay opt-in per scope (above). `workspace resolve [<path>] [--provider <p>]` shows which
binding wins for a folder (for one provider, or each; a `cli`-mode provider is labelled `MCP mode: cli`, since `cartographer call` runs the bridge for it and its bindings narrow it), says when it is derived from a KB's `paths.yaml` (D1019), and, for one that no binding covers, prints `access: read-only (default)`, `access: read-only (unbound_access)` or `access: read-write (unbound_access)`. `workspace unbind` on a binding derived from a KB's `paths.yaml` only prints the note and exits 1: it saves nothing. `workspace bind --kb` with no value, and a provider name `list` or `resolve --provider` does not know, are usage errors (exit 2). `workspace bind … --worktrees` records the opt-in to the repository's linked git worktrees on the binding (`worktrees: true`; a rebind without the flag turns it off; the bind prints `(and its linked worktrees)`, `workspace list` shows `[worktrees]`); `sync` then projects into each linked worktree, prunes the vanished ones silently and `status` lists them (`docs/sync.md`, the Linked git worktrees paragraph). `workspace list` shows the bindings, with `[gone]` next to a
directory that is not there any more and `[derived: paths.yaml of kb-a]` next to one a KB's declared repository implies, then the declared repositories with no local clone (D737, `sync.md` §Workspace scope). Limits: the bridge opens no
server-push stream; if `cartographer` is not on `PATH` when `sync` runs, the provider keeps its HTTP
entry with a warning; a client with `roots` in an unbound folder gets a handshake naming no KB.

**`cartographer mcp exec <kb>/<slug>`.** Starts the approved `mcp/<slug>.json` of a KB with its `${VAR}` and
`${secret:<concept>#NAME}` references resolved into the child's environment: it reads the descriptor and each
secret from Meridian (`secret_resolve` with `reveal`, so an `rw` token that allows secrets; every start is a
reveal), refuses a descriptor that is not approved or changed since, and replaces itself with the command
(Windows: a parent that kills the child on interrupt, ends it with the launcher through a job object, and returns its exit code). Nothing is cached: an unreachable
server stops with one line on stderr and starts nothing. stdout is the server's. An `http` descriptor is bridged: stdin/stdout stay the client's, each JSON-RPC line is POSTed to the
URL with the resolved headers (session id and protocol version echoed, JSON or SSE answers one line each, DELETE on EOF), a
transport error on a request becomes a JSON-RPC error (-32000) free of any header value, redirects and plain `http://` to a
non-loopback host are refused. Exit codes: 2 for a usage error or a ref that is not `<kb>/<slug>`, 1 for everything else (not approved, server unreachable, secret refused, cannot start). The launcher keeps the Cartographer token variable for its own calls: the child does not inherit it and a `${...}` naming it is refused; a variable the descriptor sets replaces the inherited one. `sync` writes this entry as below ([D2037](decisions/D2037-mcp-secret-launcher.md)).

**What a client gets for a KB `mcp` descriptor.** "native" is the client's own entry with its `${VAR}` syntax,
"launcher" is `<absolute cartographer> mcp exec <kb>/<slug>` as a stdio entry (no secret, header or URL in the file),
"bridge" the same entry for an `http` descriptor the client cannot give headers to. `${secret:}` always uses the launcher.

| Client | http + `${VAR}` header | http + secret | stdio + `${VAR}` | stdio + secret |
|---|---|---|---|---|
| Claude Code, OpenCode, Antigravity, Crush, Copilot, Pi | native | launcher (bridge) | native | launcher |
| Codex | native (`Authorization: Bearer` only, others dropped with a warning) | launcher (bridge) | native | launcher (`env_vars` lists the token and `${VAR}` names) |
| Kiro, Cline (`SupportsMCPHeaders` false) | bridge | bridge | native | launcher |

Without a `cartographer` command on `PATH` a descriptor that needs the launcher is skipped with a warning, and so is a descriptor of a plugin (a non-KB source) that needs it: the launcher reads `mcp/<slug>.json` of a KB, which a plugin has none of.

**`cartographer mcp probe [--json] [--timeout <d>] (<kb>/<slug> | <slug>)` (diagnose an MCP descriptor).** For an approved MCP descriptor it reports, without a client, the transport and target, the `${VAR}` references unset or empty in the shell that runs it (a client started from another shell or the dock may see another environment; `${secret:...}` is the launcher's, never listed) and, for an `http` descriptor, a DNS + TCP + TLS dial with a 15 s limit that classifies the failure (`dns`, `refused`, `timeout`, `tls_unknown_authority` with the hint to serve the full chain or set `NODE_EXTRA_CA_CERTS`, `tls_other`). When those pass it runs the session: it resolves the descriptor exactly as `mcp exec` does (`mcpexec.Resolve`: the descriptor read from the server, the approval checked, `${VAR}` and `${secret:...}` expanded, in this process), starts the command or reaches the URL with the resolved headers, runs `initialize` and `tools/list` within `--timeout` (default 20 s) and prints the protocol, the server and the first 10 tool names. A stdio child that exits before answering reports the last 20 lines of its stderr, with every resolved value replaced by `<redacted>`. A network or TLS fault met by the session keeps its class (exit `4`/`5`). A bare `<slug>` works when one KB ships it. Values are never printed. Exit codes: `0` ok, `1` the server is not usable (the descriptor cannot be resolved, `initialize` or `tools/list` failed, the child died), `2` usage error, descriptor not found, ambiguous or not approved, `3` unresolved `${VAR}`, `4` network (DNS, refused, timeout), `5` TLS. A descriptor with `requires: <label>` whose host did not answer (dns, refused, timeout) reads `needs <label>: <host> did not answer` and sets `needs` in `--json`. `--json` prints one stable object. Why: [D2042](decisions/D2042-mcp-probe-and-requires.md).

**`cartographer call` (no MCP).** For an agent that has a shell but cannot use MCP,
`cartographer call --provider <p> <tool> [key=value…] [--json '<object>'|-]` sends one stateless
`tools/call` and exits; `--list` prints `name<TAB>first sentence` per tool, `--describe <tool>` the
description and input schema (`--describe a,b` several tools at once: one JSON array in the order asked,
one `tools/list`; an empty, repeated or unknown name is exit 2 with nothing on stdout). It drives the same bridge as `cartographer mcp`, so the scope is
identical: the provider plus the working directory (`--dir` overrides it), with the unbound read-only
default and `knowledge_bases` access. A value that parses as JSON (`limit=5`, `ids=["a"]`, `title="2026"`)
is passed typed, anything else as a string, then retyped by the tool's input schema (one `tools/list`,
fetched when the call has `key=value` words): a `string` property keeps the raw text (`query=2026`,
`query=null`), an `array` property wraps a value that is not an array (`where=status=open`); a key the
schema does not declare, or a bridge that cannot list tools, keeps the parsed value. `--json` takes an
object (`-` reads stdin, 16 MiB) and stays verbatim; a key given twice, in pairs or with `--json`, is a
usage error. No other client-side validation: Meridian's own error is what the agent reads. An unknown
`--provider` and a `--dir` that is not a directory are refused before the bridge starts (exit 2).
Flags: `--output text|json` (`json` is the raw MCP result),
`--timeout` (default 5m, bounds the whole invocation), `--verbose` (print the bridge log on success too).

`--instructions` prints what the server sends an MCP client at `initialize` (`result.instructions`;
`--output json` the raw result). The bridge normally declares its session provisioned, and the server
then withholds the operating guide (D1097); this mode asks for it (`Bridge.Guide`), as a client with no
managed instructions block would. An empty guide is exit 2; a folder bound to no KB prints the bridge's
own answer.

`--batch -` runs many requests through one bridge: JSON lines on stdin (`{"tool": "<name>", "arguments":
{...}, "id": <any>}`; `arguments` and `id` optional, any other key is an error), one JSON result per
non-empty line on stdout, in input order and as soon as each is ready. Arguments are sent verbatim (no
`key=value` words, no schema fetch). Requests run one at a time, so a write is seen by the read after
it; a result line has `line` (1-based, blank lines count), `id` (echoed), `tool`, `ok` and `text`
(`--output json`: `result`) or `error` = `{code, message}`, `code` being the exit class a single call
would have (1 the tool or its arguments, 2 anything else). A failing or malformed line yields its error
and the batch continues; the exit code is the worst class among the lines, 2 also when the batch stopped
early (a line over 16 MiB, `--timeout` or an interrupt: the line in flight and the next one read get a
code-2 result, the rest of the input is not read). An input with no request is exit 2 and starts no
bridge. Scope, token, author rule and plugin tools are those of a single call; a plugin's tools work in a
batch. `--batch` excludes a tool, `--json`, `--list`, `--describe` and `--instructions`.

| Exit | Meaning | Output |
|---|---|---|
| 0 | the tool succeeded | text content on stdout |
| 1 | the tool returned `isError`, or Meridian answered "invalid params" (JSON-RPC -32602 for a bad argument) | its text on stderr, nothing on stdout |
| 2 | usage, no client configuration, unusable token, no KB in scope, transport, server too old for read-only, HTTP 401, any other JSON-RPC error (an unknown tool included), timeout | `Error: …` on stderr |

**`cartographer docs` (the documentation of this version).** The binary embeds the documentation of its own
commit as a KB (`README.md`, `CONTRIBUTING.md`, `docs/**`; generated by `internal/docskb`, name
`cartographer-docs`). `cartographer docs <tool> [key=value…] [--json '<object>'|-]`, `--list`,
`--describe <tool>[,<tool>…]` and `--batch -` behave as in `cartographer call` (same typing of
`key=value` words, same `--output`, `--timeout`, `--verbose` and exit codes 0, 1, 2), but the tools are the
nine of the read-min profile (D1184) run in this process: no provider, `--dir`, server, network, client
configuration or mount, and a write tool is refused `read_only_profile`. The start is
`cartographer docs graph_context query="<question>"`, then `cartographer docs concept_read id=<id>`; with no
argument it prints that usage (exit 0). It is not write-free like `call`: the KB opens on a directory and the
tools keep state beside it, so each invocation materializes the tree in a private `cartographer-docs-*`
directory under the OS temp directory (never `$HOME`; works with a read-only one), removed on exit;
leftovers of a killed process older than 24 h are swept by age. `cartographer docs export <dir>` writes the
KB tree into a missing or empty directory (exit 0, prints the file count; a non-empty directory is exit 2).
Links to code in the generated pages point at the repository at the build's release tag, or `main` for a
build that is not one.

`--batch`: the worst class among the lines; the per-line codes are in the result lines.

**The command writes no file anywhere** (no cache, log, lock or audit file, `$TMPDIR` included; a batch and `--instructions` too) and
works with a read-only `$HOME`: it only reads the client configuration and the token. The bundled
`cartographer-cli` skill, which teaches this command, is filtered out of every provider's manifest
client-side unless the provider reaches Cartographer through the command; no built-in provider does
today.

Existing configurations are untouched: the scope is per provider, absent means the historical global
catalogue, and no upgrade changes it. Full rules, the project-local destination matrix, the
repository-hygiene guarantees and the providers that cannot be scoped at all → `sync.md`
§Workspace scope.

**One routed entry (D187, D288).** When `/health` reports `routed_path` (every server since D288),
the KB is no longer part of the entry name: Compass writes **one** entry per provider,
`<server_name>`, pointed at `/mcp/routed`. The provider's binding travels in the URL: an explicit
binding writes `?kbs=<bound KBs, sorted, comma-joined>`, which Meridian turns into that connection's
KB set (two or more KBs: `kb` is a required enum in every schema; exactly one: no `kb` argument at
all); the default binding, every KB Meridian mounts, writes no `kbs`, so a KB added later reaches the
provider without a rewrite. Every routed entry ends with `provisioned=1` (`/mcp/routed?provisioned=1`,
`/mcp/routed?kbs=kb-a,work-kb&provisioned=1`; D1097): Compass has the managed block, so Meridian sends
it no operating guide in `initialize`. The marker is always written, a server that predates it ignores it,
and `mcpbridge` adds it to the URL it forwards to. One `sync` adds it to an entry written before. A bound name Meridian no longer mounts is left out of the URL (Meridian
refuses an unknown name), and a provider bound only to vanished KBs gets no entry, the same declaration
as an explicit empty binding. `kbs` only narrows, never widens: the per-KB authorization still runs at
the target, and with `auth: false` the binding is advisory, since the agent client can edit its own URL.
The generated instructions block names the bare tools and says to pass `kb: "<name>"` where a tool
takes a `kb` argument, which is true for both shapes. The topology is persisted in
`.cartographer-state.json` (`server_mount_mode`, `server_routed_path`) so `doctor` and `status` can derive
the expected entries offline.

**`sync` heals a topology change; no reconnect is needed.** A client whose state predates D288 holds
per-KB entries (`<server_name>-<kb> → /mcp?kb=<kb>`). The next `cartographer sync` removes every name the
client may have owned (the bare name and one per known KB, including unbound ones) and writes the single
routed entry, so one sync leaves exactly one entry per provider and no orphan. `cartographer status`
reports `mount mode changed: …` and names `cartographer sync` until then. Against a server that does
**not** report `routed_path` (pre-D288) Compass keeps the historical per-KB shape, with the two
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
during `sync`, remote `reindex`) qualifies the tool name with the prefix the
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

The provider (and, for `kb` subcommands, the KB name) may be written before, between or after the flags
(`connect claude --kb all`, `connect --kb all claude`); a second positional is a usage error naming it
(exit 2), never ignored. A `--server-url` ending in `/mcp/<segment>` that fails `/health` with 404 is answered with a hint that the server URL is the one ending in `/mcp`.

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `antigravity` \| `crush` \| `copilot` \| `pi` \| `cline` \| `all` (all detected agents) |
| `--agents` | *(unset)* | Comma-separated subset (`claude,codex`); cannot be combined with the positional provider |
| `--kb` | *(unset)* | Which KBs this client may receive (repeatable, or comma-separated; `all` for every mounted KB). Required on a **first** connect against a server mounting two or more KBs — see below (D190) |
| `--server-url` | `http://127.0.0.1:39273/mcp` | Cartographer server URL |
| `--auth` | `false` | Enables the Bearer header in generated configs |
| `--token-env` | `CARTOGRAPHER_TOKENS` | Name of the env var holding the Bearer token; written as `token: ${NAME}`. Anything that is not a variable name (`$NAME`, `my-token`, a pasted token) is refused (exit 2), here and in the form |
| `--dry-run` | `false` | Prints what would be written, in the conditional (`would write`, `would connect`), and writes nothing ([D147](decisions/D147-every-reported-write-is-observed-never-intended.md)) |
| `--allow-insecure-http` | `false` | Accepts a plain `http://` server URL that is not loopback and records `server.allow_insecure_http: true`; without it `connect` refuses such a URL (§Transport security, D1023) |
| `--no-tray` | `false` | Does not install the status icon and remembers it (`tray_opt_out`) until `service tray install`; an installed tray is removed. Without it, `connect` installs the tray on macOS and Windows (below) |
| `--pin-key` | *(repeatable)* | Pins `KB=PUBLIC_KEY` for Ed25519-verified provisioning artifacts; existing pins are preserved |
| `--unpin-key` | *(repeatable)* | Removes the pinned keys of a KB from `.cartographer.yaml`; applied before `--pin-key` (so both replace a pin), and a KB with no pin is an error. A pin that does not verify Meridian's artifacts fails `connect` with a signature error (exit 2, nothing written: the manifest is fetched and verified before the agents' MCP entries, the bootstrap hook or `.cartographer.yaml` are touched, and the same holds for a rejected token), not a "server unreachable" deferral; a server that is down still defers and writes |
| `--no-input` | `false` | Never opens the interactive form, even in a TTY |
| `--workspace` | *(unset)* | Projects the selected KBs into this repository instead of the global catalogue (D193) |
| `--plugin` | *(repeatable)* | Installs a plugin (git URL`[@ref]` or folder) before the agents are resolved; it is not approved by this flag |
| `--approve-plugins` | `false` | Approves the plugins installed by this run, after printing what each one runs; the flag is the consent, the printed block its record |
| `--auto-trust` | `false` | Accepted and ignored, with a warning (D906) |

**An unloadable `.cartographer.yaml` is never replaced** (D1514). `reindex`, `kb repair`, `reconnect`, `client` and `usage` print that error too, instead of "no client config found" or a silent fallback to the local default server. `connect` and `disconnect` fall back to the defaults only when the file does not exist. Any other `Load` error (malformed YAML, a rejected `providers.<p>.access`, `config_dirs` outside claude, an unreadable file) stops the command before any write, naming the file and the error (`fix or remove <path>`); `Save` itself refuses to replace an existing file it cannot parse. `status` and `doctor` print the same error instead of sending the operator to `connect`.

### Transport security (D1023)

The bearer token and the responses must not cross the network in clear. Every
client request goes through `client.CheckServerURL`: `https://` is always
accepted; `http://` only when the host is loopback (`localhost`, or an IP for
which `net.ParseIP(host).IsLoopback()` holds: `127.0.0.0/8`, `::1`, IPv4-mapped
`::ffff:127.0.0.1`; a name such as `127.0.0.1.example.com` is not); any other
scheme is refused. The check is in the Compass transport, so `connect`, `sync`,
`status`, `doctor`, `kb repair`, `kb reauthor`, `kb secrets init`, `reindex`, the usage sync and the stdio bridge
all obey it, and `connect` and `sync` fail before writing anything.

The explicit opt-in is the `server.allow_insecure_http: true` key, set by
`connect --allow-insecure-http` (re-run `connect` with the same flags; it only
adds the key), or `CARTOGRAPHER_ALLOW_INSECURE_HTTP=1` for one run. A machine
already connected to a plain-http remote is refused at its next `sync` with an
error that names both fixes. Nothing upgrades `http` to `https` by itself.

`knowledge_bases.<kb>.signing_keys` in `.cartographer.yaml` stores public-key pins per KB. Pins are
operator-supplied and are never learned from `sync_pull`; multiple pins permit
key rotation. Pin the new key, switch the Meridian signer, then remove the old
pin after all clients have synchronized.

If no provider is detected and no explicit name is passed, the command exits with an error
(exit 1) without writing anything.

**Interactive form (TTY, D49+D64+D86).** With no form flags and in a TTY, the connect form
opens (`connectform.go`): each field shows a contextual hint below it when focused
("Token env var" is the **name** of the environment variable holding the bearer token — the token
itself is not entered here, the form saves only the name; with Auth off the field is rendered secondary and the hint says it is
ignored). In the `connect` form, one checkbox per provider is pre-selected from the
installed-agent set; select one or more with Space or Enter. The Server URL prefill follows the precedence existing `.cartographer.yaml` >
`CARTOGRAPHER_SERVER_URL` (client env) > `http://127.0.0.1:39273/mcp` — the loopback literal the local service listens on, never `localhost`, which Windows resolves to `::1` first, where nothing listens (D231). A `.cartographer.yaml` still carrying the old default `http://localhost:39273/...` is read back on `127.0.0.1` (path kept), so the next `connect` or `sync` rewrites every client's MCP entry; any other URL is left as written. On submit a **probe** runs
(`client.Health`, `GET /health`, 5s timeout, anonymous first and then with the token from env, only if Auth is enabled and the anonymous answer is a Cartographer server's) before writing
any file: a reachable server with no mounted KB explains the `kb create` then service-restart path;
otherwise on failure the form is re-shown with the entered values and an inline error
(distinguishing a 401 "token rejected" from a network error), with an override available — in CLI a
`y/N` prompt "proceed anyway?". A failed `doConnect` also re-shows the form populated (connect is idempotent: no
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
`cartographer service install` when Meridian does not answer at all (never when `/health` replied, e.g. a 404 on a `/mcp/<segment>` URL); a reachable 0-KB server instead prints
`cartographer kb create <name> --remote <url>` followed by `cartographer service restart`. A successful connect
prints the absolute paths of generated MCP configs and reminds the user to restart the selected
agent sessions to load the MCP tools (a `cli` provider is told to restart to load the skills and instructions, a `none` provider gets no line).

`connect` also installs the status icon (`cartographer tray`) on macOS and Windows, after the sync timer: a tray already installed is left running as it is (a `connect` never restarts it; `upgrade-repair` refreshes it), a remembered opt-out installs nothing, and a failure (a session with no GUI such as ssh) is a warning that names `cartographer service tray install`, never a different exit code. A dry run prints `[dry-run] would install the status icon`. `reconnect` and `setup` go through the same step (the `setup` plan lists `status icon  install`); `sync` never installs it.

### `cartographer disconnect [provider|all]`

The inverse of `connect`: for each target provider — default `all` = every provider **connected**
in the Compass state — it surgically removes every managed MCP server entry (the bare name and
any persisted per-KB suffixed names) from that provider's
config file (`internal/configurator.Remove`, the inverse non-destructive merge: the rest of the
file is left intact; an emptied `mcpServers`/`mcp` map is dropped, and the file itself is deleted only when it is left an empty shell and the provider's registry entry sets `DeletableWhenEmpty`: `.claude.json` and the Claude Code hook settings files go only when Cartographer created them (recorded in `.cartographer-created.json` in the base dir, itself deleted with its last entry) and removing our entries leaves `{}`; a file that existed before, even as `{}`, or that holds another key, is kept), prunes the
managed artifacts registered for that provider in the lockfile (`provisioning.PruneManaged` — only
managed files, never untracked ones) and what its **workspace projections** wrote in each bound
repository (the files the lockfile records for them, exactly as `workspace unbind` + `sync` would; what
was there before stays, a skill, agent or hook that no longer hashes to what the sync wrote stays and is reported
as `kept <path>: changed since sync`, a lockfile path that is not inside the repository is never followed, and a file
another provider's projection of the same repository still records stays too), prints which repositories it cleaned, then removes the provider from the
lockfile and from the Compass state, its workspace bindings and projection scope included (#1324). If the lockfile ends up with no providers it is removed; `.cartographer.yaml`,
on the other hand, is **never deleted** (D64): with zero agents it stays on disk with an empty agent list in the state file,
preserving `server:` (`url`/`name`/`auth`/`token`) and `providers` as defaults for the next
`connect` (a disconnect→connect restarts from the previous server, not from `http://127.0.0.1:39273/mcp`).
The Compass state files also stay after a disconnect: `.cartographer.yaml`, `.cartographer-state.json`, `.cartographer-machine.json`,
`.cartographer-client.lock` (in the config directory), `.cartographer-created.json` (in the home or workspace, only while a Claude Code file it created is kept because it holds other keys) and `workspace-derived.json` (in the cache directory, `~/.config/cartographer/`). `.cartographer-client.lock` is an empty lock file, not state: the first sync or connect creates it and a dry run does not announce it. To remove every trace, delete them by hand once the last provider is disconnected.

```bash
cartographer disconnect                # every connected provider
cartographer disconnect claude         # Claude Code only
cartographer disconnect --agents claude,codex # selected subset
cartographer disconnect all --dry-run  # preview without writing
```

| Flag | Default | Description |
|------|---------|-------------|
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `antigravity` \| `crush` \| `copilot` \| `pi` \| `cline` \| `all` (every connected provider) |
| `--agents` | *(unset)* | Comma-separated subset (`claude,codex`); cannot be combined with the positional provider |
| `--dry-run` | `false` | Prints without removing |

When the disconnect leaves no agent connected and no local service is installed, the status icon is removed too (`removed the status icon: no agent is connected`): it has no server left to show. That is not an opt-out (`tray_opt_out` stays as it was), so the next `connect` installs it again; `reconnect` keeps it, and a dry run removes nothing.

Idempotent: exit 0 even if there was nothing to remove (no `.cartographer.yaml`, provider
already disconnected, MCP entry already absent, ...). Exit 2 only on an actual error (I/O, malformed
provider config JSON, ...). Exit 1 when a provider was kept connected because its config or managed
files sit behind a symlink (§Symlinked config files and directories are refused unless listed); the other providers
are still disconnected.

### `cartographer feedback --provider <name> --kind bug|idea [--title <text>] [--file <path>|-] [--dir <folder>] [--output text|json] [--timeout 30s]`

Prints a redacted bug or idea report and the link that opens it prefilled (D1925). The draft comes from `--file` or stdin (capped at 64 KiB). Without `--title`, a first line `# <title>` is the title and is removed from the body, so an agent puts nothing it wrote on the command line. `--timeout` is one deadline for all the server calls together. It **sends nothing, opens no browser and writes no file**: its only network use is Meridian, to read `kb_status` (server version, `latest_version`, the `feedback` destination) and `concept_list` (limit 500) of the KBs the provider is bound to. Every real value the client knows is replaced by `internal/feedback`'s redactor: the KB names (bound, known, served, bound to a workspace), the server and workspace-remote hosts, the OS user, the home and workspace paths, the concept IDs, the client's own token, and anything `internal/secretscan` or the token-shaped patterns flag. A source that cannot be read (the config, the server, a `concept_list`, a list over 500) is named in a `Note:` line and the command goes on; with the server down the destination is GitHub. The output is `Title:`, `Body:` (the redacted draft plus a context block, unless the draft has its own "Version and transport" section), `Removed:` (counts per class and secret kinds, never a value), for GitHub a `Search first:` link before the `Submit:` link, and "Nothing has been sent". If the server is behind the latest release it says to update first. With a custom destination only the operator's template link is printed; when the body does not fit the link the title-only link is printed and the body is pasted from the output. `--output json` prints `{title, body, removed, search_url, url, body_in_url, destination, notes}`. Exit codes: 0 draft printed, 2 usage, 3 the operator switched feedback off (nothing on stdout), 4 empty draft. The command cannot judge prose or security matters: the bundled `cartographer-feedback` skill keeps the model pass and the user's approval, and stops on security matters before calling it.

### `cartographer status`

Compares Meridian's manifest revision with the last applied lockfile, for every connected
provider. Read-only.

```bash
cartographer status
```

Exit code: `0` all providers in sync, `1` at least one provider in drift, `2` error (no
`.cartographer.yaml`, server unreachable, ...). For every provider it also prints per-kind
counts (`provisioning.KindCounts`), e.g. `skill 4/5 · agent 2/2 · hook 1/1`. On drift it
names a projection that is out of sync with nothing to list under it (`drift_reason` in `--output json`, #2105): a workspace projection sync never applied, or whose manifest revision differs from its lock's, which the header's global revisions do not show; a drift with no itemized cause at all says so and points to `cartographer sync`. On
drift it also prints the diff (added/updated/removed, with a `trust` state: `built_in`, `verified`, `trusted`,
`approved`, `approval_stale`, `needs_approval` or `plugin` (an artifact a plugin contributes: gated by the plugin's own approval, never a per-artifact one) — see [D115](decisions/D115-mcp-allow-list-and-hash-bound-local-approval.md)
for the MCP-specific approval states; an unsigned KB artifact that is not an MCP descriptor, a hook or an executable file is `trusted`: it is installed,
D906). Artifacts in `needs_approval`/`approval_stale` get their own
`cartographer approve <kind> <name> --kb <kb>` hint. Every pending artifact kind carries its trust state in `--output json` (`trust`), not only MCP descriptors. A pending hook also names the file the sync registers it in, resolved by the function the sync prints with (a derived workspace binding: `.claude/settings.local.json`, #1300). Before the artifact report it prints the
client and server versions. A non-`dev` mismatch is a warning only (it does not change the exit
code); on loopback, an installed local service also gets a `cartographer upgrade-repair` hint
(D121: it replaces the running service **and** re-syncs the providers in place, where the former
`service restart` hint only did the first half).
On macOS and Windows a table `status` of a client with a connected agent, no status icon installed and no remembered opt-out ends with one line pointing at `cartographer service tray install` (`status` installs nothing; `service tray uninstall` silences it; the JSON output is unchanged).
A newer release known to the update cache adds `update available: vX (installed vY) — <command>`
(JSON `update: {latest, kind, channel, command}`, present only then), and a remote server that
reports one in `/health` adds `server update available: vX` — the Meridian operator's job, so no
command. Both read local state only (the cache is refreshed by the session hook and `update
check`) and neither changes the exit code (D254). A KB the server was told to serve and could not
mount adds `KB not mounted: <name> — <reason>` per KB (JSON `skipped_kbs: [{name, reason, retrying?}]`,
from `/health`); it never changes the exit code either.
A KB whose commits the server could not push adds `KB <name>: 3 commits not pushed, <remedy>` (`push failed, <remedy>` when the count is unknown; JSON `push_lag`, a list of those lines, present only then), read from the `push` object of the KB's `/health` entry (`docs/deployment.md` §Observability). It is information like the update line: the commits are safe in the server's clone, so the exit code and `state` do not change.
Placeholder keys the last sync could not resolve and the operator did not `paths ignore` (read from the lockfile, no network) add
``N placeholder(s) unresolved — run `cartographer paths` `` to the table and an additive
`unresolved_placeholders: [{key, kbs, reason}]` to the JSON, present only when non-empty; neither
changes the state or the exit code — an unresolved key is left verbatim, not a failed sync (D262).
The artifacts Meridian left out of the catalogue (the `sync_pull` `issues`, `sync.md`) add one `not in the catalogue: …` line each and an additive
`issues: [string]` to the JSON, present only when non-empty, raw like every server text in the JSON (the table escapes it); they change neither the state nor the exit code (#1775). `connect` reports them among its warnings.
An agent whose frontmatter is not valid YAML is in the catalogue, held for approval: `status` lists it as `+ agent/<name> … trust=needs_approval` with an extra `held for approval: its frontmatter is not valid YAML (<parse error>), …` line (JSON: `held_reason` on the artifact, present only then, derived client-side from the pulled file) and leaves its manifest issue out of the `not in the catalogue` lines and the JSON `issues`; `sync` and `connect` still print that issue as a warning (#1984). The same issue about an agent in the catalogue that `status` does not show as held (its content is approved, or it is installed and in sync) stays as an `in the catalogue, with a problem: …` line and an additive `catalogue_warnings: [string]` in the JSON, present only when non-empty and raw like `issues`; it never reads "not in the catalogue" (D2017).
For an unavailable endpoint, the table names the configured endpoint once and
suggests checking that URL (or `cartographer service status` for loopback);
connected providers are reported as `unknown`, rather than repeating a network
failure for each provider.
A server that answers `/health` but is still bootstrapping its KBs is not unreachable: the
snapshot says `reachable: true`, `ready: false` and `error: {code: "starting", message: "the
server is starting and loading its KBs; retry shortly"}` (state `unavailable`, exit `2`), and the
table prints that message instead of the endpoint advice (#1305).
A server that answers `/health` but then fails the manifest pull is not unreachable either: the
snapshot keeps `reachable: true` and carries `error.code` `signature_failed` (the manifest does not
verify against the pinned keys), `unauthorized` (401 or 403) or `pull_failed` (any other failed
`sync_pull`, the cause in the message), and the table prints the message and the cause on stderr
(state `unavailable`, exit `2`). `request_failed`, `dns_failed` and `unreachable` stay for a server
that cannot be reached.
A managed artifact that diverged on disk is listed as `diverged on disk (<reason>)`; for an
`unrunnable` hook — a Claude Code hook whose registered command cannot run, such as a backslash
path on Windows or a file that is not there — the next line says which command and why (JSON
`detail`), and `sync` re-registers it (D267).
A provider's stdio bridge entry (D737) is listed the same way, as `mcp/<server>`, when its `command` is
not the `cartographer` that `sync` would find on `PATH` now (the binary was moved or removed since
`connect`, as after trying a release candidate from `$HOME/.local/bin`): `status` exits `1` and `sync`
or `upgrade-repair` rewrites it. An entry that is not the bridge (an HTTP entry, or one written by hand
under the same name with other arguments) is not judged.

### `cartographer sync`

Re-runs `sync_pull` and reapplies the manifest for every connected provider: materializes
add/update, prunes obsolete artifacts, updates the lockfile. Idempotent.

```bash
cartographer sync [--client <provider>]... [--dry-run] [--no-heal] [--log-file <path>] [--report-to <url>]
```

`sync`, `upgrade-repair` and `import` take flags only: a positional (`sync claude --dry-run`) is a usage error naming it, exit 2, never a silent full sync (#1502). Use `--client`.

| Flag | Default | Effect |
|---|---|---|
| `--client` | *(all)* | Syncs only this provider (repeatable). A provider left out is not touched at all: not its MCP entries, not its artifacts, not its lockfile entry |
| `--dry-run` | `false` | Prints the plan without writing: every artifact and MCP entry, the `.cartographer.yaml` rewrite (only when its bytes would change) and the creation or update of `workspace-derived.json`; MCP entries are listed for removal only when they exist in the config |
| `--no-heal` | `false` | Reports managed artifacts that diverged on disk instead of restoring them from Meridian (D139) |
| `--log-file` | *(none)* | Appends this run's output to that file instead of stdout/stderr (created if absent, never rotated); the Windows sync task uses it, where nothing else records a failing background run (D217); on Windows a run that is the only process on its console releases it, so the task's window closes ([D1479](decisions/D1479-the-windows-service-detaches-from-its-console.md)) |
| `--auto-trust` | `false` | Accepted and ignored, with a warning (D906): session-start hooks written by older versions still pass it |
| `--report-to` | *(none)* | Only sends this machine's usage and client-settings reports (D326, D818) to that MCP endpoint, and changes nothing: the config is read, never saved, nothing is provisioned, the configured server is not contacted. It is how `make dev DATA=<dir>` shows the real client on a live instance (D1044). The configured token goes with the reports, so the URL must name a server on this machine (`localhost`, `127.0.0.1`, `::1`): any other host is refused with exit 2 |

For Claude Code the `cartographer` MCP entries are written to `$HOME/.claude.json` and to
`<dir>/.claude.json` for every `providers.claude.config_dirs` (D755), and removed from all of them.
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

Before anything else — before the first network call, `--dry-run`
included — `sync` checks the environment it needs: when `.cartographer.yaml` has `auth: true` with a
`token: ${NAME}` reference, that variable **or the token file** (D699); a literal token needs nothing here. Missing ones are reported in one error naming each, so an
unattended run (session-start hook, sync timer, CI — none of which inherit a login shell's exports)
diagnoses its misconfiguration before the first network call
([D222](decisions/D222-sync-checks-the-environment-once-and-the-401-names.md)). The check is
read-only and never contacts Meridian: it verifies a token *exists*, not that Meridian accepts
it — a token that is set but wrong is still a 401 from `sync_pull`. When nothing is missing it
prints nothing. 

Every sync verifies the managed files on disk, not just the manifest revision, and restores what
was edited or deleted locally — the restore is reported on its own line, because it discards
someone's local change. See [`sync.md`](sync.md) §On-disk verification and healing.

This is also how a configured provider is repaired **in place** after a local upgrade: the same
in-process runner is what `cartographer upgrade-repair` calls (D121). Repair in place is the
default; a full rebuild is `cartographer reconnect` (below), for the residues no incremental sync
can see. Already-open provider sessions still need to be restarted to reopen the MCP connection.

When Meridian that answers is not the one this client's state was materialized against, `sync`
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
| (positional) | `all` | `claude` \| `opencode` \| `codex` \| `kiro` \| `antigravity` \| `crush` \| `copilot` \| `pi` \| `cline` \| `all` (every connected provider) |
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
cartographer doctor [--output table|json] [--provider claude]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--output` | `table` | `json` emits the report as JSON (`cartographer.doctor/v1`) instead of text, like `agents`, `status` and `update check` |
| `--json` | `false` | Deprecated alias of `--output json`, kept working |
| `--provider` | *(unset)* | Narrow the run to one provider — inspected even if it is not connected |

**It never repairs, and never writes** — no lockfile migration, no directory creation, no cache
refresh — except `--repair-hashes` below, the one flag that rewrites the lockfile. Every finding names a real path on this machine and the command that fixes it (`sync`,
`reconnect`, `connect`, `service sync-timer install`), because a diagnosis nobody can act on is
noise and a doctor that silently fixes things is a doctor nobody can predict. One check, `push-access`, uses the network (a push dry run to each KB's remote, in a temporary repository); it is skipped with a note when the remote does not answer or the probe cannot be judged.

The checks:

| Check | What it looks at |
|---|---|
| `client-config` | `.cartographer.yaml` exists and loads (an existing file that does not load is reported with the real error and the fix `fix or remove <path>`, not `connect`); an agent is connected; every configured provider is still installed |
| `lockfile` | present, readable, and in the v2 format — a v1 file on disk is migrated *in memory* by every read, but stays v1 until something rewrites it |
| `managed-files` | the on-disk verification of D139, per provider: `missing`, `modified`, `unregistered` (also a hook of Claude Code, Codex, Antigravity or Kiro whose registration was deleted, when its `hook.json` loads and the client fires its event, #2101), `unrunnable` (a Claude Code hook whose registered command cannot run, with the reason — D267); `foreign` (an info line: a file inside a managed skill/hook directory that the lock does not own, which `sync` leaves and `status` does not count as drift, #1502); plus files sitting inside a managed skill/hook directory that no lock entry accounts for (D178) — reported only, since doctor cannot prove Cartographer wrote them |
| `mcp-entries` | the Cartographer entries in the provider's native config match the KBs recorded in `.cartographer.yaml` — an entry for a KB Meridian no longer mounts, or a missing one. When the config is behind a symlink the fix names the link (replace it with a regular file or directory, or list it with `cartographer client symlinks <provider> --add <link>`) instead of `cartographer reconnect`, which refuses an unlisted link too (D148, D1736). Only the names `disconnect`/`reconnect` manage count (the server name and `<server>-<kb>` for the recorded KBs): a user's own `cartographer-dev` is left alone |
| `mcp-variables` | an approved KB MCP descriptor references a `${VAR}` that is unset or empty in the environment doctor runs in: a client started from a shell without it shows "Connection closed". Warning severity, one per descriptor, names only (a `${secret:...}` is the launcher's and never listed); the fix is `cartographer mcp probe <kb>/<slug>`. Silent when nothing is approved or the server cannot be asked for its descriptors |
| `mcp-network` | an approved MCP descriptor declares `requires: <label>` and its host did not answer a 2 s dial (all such descriptors are dialed in parallel): `info` severity, "needs <label>: unreachable now", because being off the VPN is a state and not a fault. `status` shows the same as `needs <label>: unreachable now` under the artifact (JSON: `reach: "needs_network"` and `needs`, additive) |
| `instructions` | exactly one well-formed managed block per provider that has instructions materialized (begin recognized by prefix, so a block written by an older version still counts), **and** that the provider actually reads the file it was written into (D189) |
| `hooks` | one native registration per managed hook (a missing one is the `managed-files` `unregistered` finding, #2101) — the D99 double-fire is a registration left outside the managed block by Codex's own rewrite; for OpenCode, the plugin shape written against the installed major (D359) — a 1.x plugin on OpenCode 2.x is an error, an unsupported major a warning, an unreadable version a warning (the shape was guessed, D719) |
| `server` | `/health` reachable and a Cartographer server's; when another program answers on the address (a 200 without a `version`, a body that is not JSON or a 404 — the last two probed again only for a loopback `http://` URL without a path prefix, whose root `/health` is not the server's behind a proxy) the finding says `another program answers on <host:port>` with the fix to stop it or change the listen address/port (#1998), instead of `unreachable`; the checks that would send the token to that listener or trust its `/health` (capabilities, unmounted KBs, push lag, KB collisions and workspace shadows) are skipped; the recorded `server_version` (D142) against the live one; client binary against server. When an unreachable server is loopback **and** no local native service is installed, the finding names that cause and the two remedies instead of pointing at `service status`, which would only repeat `installed: false` (D174). On Windows with the WSL marker (§`cartographer service <action>`) the marker counts as the local service, and an unreachable server names the distro and `wsl -d <distro> -- cartographer service status` |
| `wsl` | inside a WSL distro only (nothing runs elsewhere): a WSL1 kernel (warning, unsupported); the server config's data dir on a Windows drive under `/mnt/` (error; move it under `$HOME`); an installed service without the `\Cartographer\WslKeepAlive` task (warning: the distro stops ~15 s after the last terminal closes; fix `cartographer service install`, or `instanceIdleTimeout=-1` under `[general]` in `.wslconfig`, which also silences it); `networkingMode=mirrored` in `.wslconfig` (info: not verified by Cartographer). What needs Windows interop is an info when interop is disabled (`deployment.md` §Windows with WSL) |
| `path-binaries` | more than one distinct `cartographer` file on `PATH` (a Homebrew copy and an `install.sh` one, a leftover): the shell may run one and the service the other. Each `PATH` directory is asked on its own (execute bit, `PATHEXT`), symlinks are resolved and a hard link counts as the same file, so Homebrew's stable link and its Caskroom target are one binary. Warning, naming each copy with the version it prints for `cartographer version` (run with a 3 s timeout; `version unknown` when it fails), the one the shell runs (first on `PATH`) and the one the installed service definition runs, or that program when it is not on `PATH`. Silent with a single binary, and then no binary is run. The fix is to keep one install, then `cartographer upgrade-repair` (#2000) |
| `unknown-keys` | a key of the client `.cartographer.yaml` or of the server `config.yaml` (the one the installed service reads) that no settings table describes and no retired entry explains: a typo does nothing and says nothing, because the loaders go past it (a key a newer release adds must not stop an older one) and the client file keeps a top-level unknown key and writes it back (a nested one, `server.ulr`, is dropped on the next save). One warning per key with its line and, when one is close, the nearest known key at that level (`ulr` → `server.url`), the fix. The keys come from the tables themselves (`clientconfig.Schema`, `config.Schema`), below a key without keys of its own (`providers`, a map of names) nothing is checked, and the retired and moved keys keep their own messages. The server logs the same line as a warning at load. The sections `auth`, `hooks`, `plugins` and `components` of the server file still refuse an unknown key at load (#2000) |
| `trigger` | every connected provider has a session hook, or the scheduled trigger is installed (D140); a timer the operator uninstalled on purpose is info (D325) |
| `push-lag` | a KB's `/health` entry carries a `push` object: its push failed, or commits stayed unpushed past `kb.PushLagThreshold` (3 minutes), so the remote and the server's clone are diverging. Warning severity; the message is the same line as `status` prints, the fix is on the remote or its credentials. Silent when the server is unreachable or predates the field |
| `push-access` | each KB of a **local** server (loopback `server_url`, the server answered) that has an `origin` is probed, at most 4 at a time, with the push dry run of `setup` (`gitx.ProbePushAccess`, [#1999](https://github.com/BeppeTemp/cartographer/issues/1999)) against the URL the server pushes to (`remote.origin.pushurl` when set): a throw-away commit in a temporary repository, so nothing is written to the remote or left in the KB's clone, with the server's credentials for the KB (credentials directory; the rest of the environment is the shell's running `doctor`). The probe has **three verdicts**, decided by what git said. *Refused* (warning, one per KB, the fix is a credential with write access, or nothing if the KB is read on purpose): the remote answered with a verdict on the credential (`gitx.IsPushCredentialVerdict`: a permanent rejection such as "Permission to … denied", "write access", a read-only deploy key, GitLab's "not allowed to push code to this project", Gitea's "permission denied for writing", a rule; a login it refused: "authentication failed", HTTP 401, a rejected ssh key; or no login at all, "could not read Username"/terminal prompts disabled, since the server would not push either). *Offline* (info): the remote did not answer (the probe's 20 s deadline, or git/ssh printing a DNS, route, refused-connection, "network is down" or reset/closed-connection failure, `gitx.IsRemoteUnreachable`). *Could not check* (info): any other failure (untrusted TLS certificate, host key not in `known_hosts`, HTTP 429/5xx, a bare 403, an unrecognised error), which says nothing about the credential; the message carries git's cause and its remedy. Offline and could-not-check results with the same cause are **one finding naming the KBs**, so a forge that is down is one line; neither changes the exit code. Silent for a server on another machine and when the server is unreachable. `kb clone` runs the same probe on the clone it made and only prints a warning for a refusal or a note for the other two (exit 0: a reader may clone on purpose); Ctrl-C during that probe prints "not checked: interrupted", not a deadline; the server start never probes |
| `capability` | every per-KB gate Meridian advertises on `/health` is on, and no KB was mounted by discovery rather than by a `kbs[]` entry (D151). Info severity: it names the setting that would change it |
| `symlink` | no managed destination directory (global, or under a workspace binding) is a symlink, or anything else that is not a plain directory — provisioning refuses to write through one, so the artifacts it would hold are not installed (D148, widened in D216); the fix is to replace it or to list it with `cartographer client symlinks <provider> --add <path>`. When the link resolves to a regular file git tracks (`CLAUDE.md -> AGENTS.md`), the fix says listing the link would make the instructions block land in that tracked file and suggests replacing the link with a real file first, adding that replacing it changes the repository when the link itself is tracked. A destination that is itself a **listed** link (`providers.<provider>.allow_symlinks`) is an `info` finding naming its target, with no fix |
| `kb-collisions` | no two KBs bound to the same provider claim one `kind`+`name` (D171). `sync` refuses outright when they do, so a machine that has not synced since the binding changed would otherwise show no symptom. Silent when the server is unreachable |
| `kb-not-mounted` | the server mounts every KB it was told to serve. A warning per KB listed in `/health` `skipped_kbs` (a moved path, a remote that cannot be cloned), with the reason; silent when the server is unreachable (#1963) |
| `workspace-shadow` | no KB agent or skill has the same name as a file a bound workspace commits (tracked by git) at the provider's project destination (`.claude/agents/<name>.md`, `.claude/skills/<name>/`...): the provider reads one of the two and which one depends on the client. Warning severity; the finding names both files. It applies in every scope, for each provider and bound workspace, and is silent when the server is unreachable or a pull failed. The fix is to rename one; a KB's `artifact_prefix` (`docs/data-plane.md` §The KB's own settings) prevents it |
| `legacy_steering_pattern` | the operator's own steering text — the provider's instructions file outside the managed block, plus, for Kiro, the other `*.md` files beside `~/.kiro/steering/cartographer.md` — names a pre-D288 prefixed tool (`<prefix>__<tool>`, where `<tool>` is a real Cartographer tool and `<prefix>` neither is `mcp` nor contains `__`, so Claude Code's `mcp__<server>__<tool>` names are not flagged), which `sync` never rewrites there. Warning severity; the fix names the unprefixed tool ([D320](decisions/D320-upgrades-and-provisioning-tell-the-user-what-changed.md)) |
| `tray_not_running` | the status icon is installed (its task or LaunchAgent exists) but has no live process: quit by the user or dead at logon. Warning; the fix is `cartographer service tray install`, which restarts it. `status` prints the same line (#1946) |
| `update_available` | a newer release is in the update cache (D254). Info severity; the fix is the channel's upgrade command. Cache only, so doctor stays offline |
| `workspace` | each workspace binding of a provider in workspace scope: the directory is there, its remote is the one bound, Codex trusts it; and, in a git repository, the untracked files under the provider's directories (hook companions such as `.codex/hooks.json` and the OpenCode plugins included) that neither the `info/exclude` block nor an ignore file covers (#1199); for a derived Claude Code binding, a tracked `.claude/settings.json` holding Cartographer hook entries, which a derived binding registers in `.claude/settings.local.json` (D1198). Warning severity; `sync` rewrites the block, and a file of the user's is committed or ignored |
| `unbound-residue` | no managed file comes from a KB no longer bound to the provider holding it (D170) — a projection predating an unbind, or a hand-edited lockfile. Only for providers with an explicit binding; a file with no recorded source (a lockfile written before D170) is unknown, not wrong, and never reported. A file that comes from `plugin:<name>` is reported for every provider, explicit binding or not, when `<name>` is no longer in the config's `plugins` ("comes from plugin x, which is no longer installed"); a configured plugin never is |

**Severities.** `error` — something is broken now (a managed file missing, a hook firing twice);
`warning` — something is stale or suboptimal (v1 lockfile, a lockfile entry whose path leaves the Compass directory and is ignored on read, no trigger for a hook-less provider, a
version difference); `info` — context that cannot be acted on by itself and never changes the exit
code (managed entries recorded before content hashes existed, which nothing can verify). Findings
are printed errors first.

**`doctor --repair-hashes`** re-records the materialized hash of every managed entry that has none, computing it from the bytes already on disk, under the client-state lock (D157). It exists because the alternative for that narrow gap, `reconnect`, prunes and rewrites **every** managed artifact on every client. A backfilled entry is marked `adopted_at` in the lockfile: it is **adopted, not verified** — nothing was compared against Meridian, so drift is detectable only from the next server-side change onward. An entry whose file is *missing* is left alone: that is real drift for `sync` to fix, not something to paper over.

**Exit codes**: `0` clean, `1` findings (error or warning), `2` an error running the command — the
same convention `status` uses. An unreachable server is one `warning` finding, not a failure:
`doctor` stays useful offline.

**JSON shape**: `{schema_version, error_count, warning_count, info_count, findings[]}`, each finding
`{check, severity, message, path, fix}`. Flat and stable — it ends up in someone's monitoring.

The bootstrap hook and the scheduled timer deliberately do **not** run it: it is an operator command,
and running every check on every session start is exactly the background cost D60 avoided by keeping
the bootstrap script silent and deterministic.

### `cartographer kb rename <old> <new> [--data <dir>] [--config <path>] [--restart]`

Renames a KB's **mount point** offline and local (D177, D754): the directory, the matching `kbs[]` entry when there is one, the KB's credential files, an alias `{from: <old>, to: <new>}` in the server state file, and this machine's client bindings, together or neither. It never contacts a server, a client or a git remote, and the git `origin` is untouched; a cross-filesystem move fails rather than copying. Details, the alias rules and `--restart` are in [deployment.md](deployment.md); `kb unmount` is its inverse.

### `cartographer kb repair <kb> [--apply] [--json | --revert <sha> [--reason <text>]]`

The unattended half of KB upkeep (D299), schedulable by the operator (cron, launchd, a CI job); nothing
installs a schedule. It calls `kb_status`, then `kb_repair` with `dry_run: true` for every check with a
mechanical fix, and prints per check the planned count and up to 5 examples. With `--apply` it re-runs
**only** the checks whose fix is safe to run unattended (D323, D355, D907: `lint.AutoRepairChecks`, `[auto]` in the
report) with `dry_run: false`; any other check is never applied by `--apply`. No setting narrows the list. Meridian applies the same checks by
itself every day, and each write already repairs the concepts it wrote
(D349, always since D907), so `--apply` usually finds little. It ends with the judgement
work left — the `kb_review` counts by kind — which only a `cartographer-kb-doctor` session with the operator handles.
It writes no `kb-doctor` log entry: a mechanical pass is not a doctor session, and the entry would
silence Meridian's doctor proposal while the judgement work is undone (each applied check still
leaves `kb_repair`'s own entry).

`--revert <sha>` replaces the repair: it calls `repair_revert`, which undoes one `kb_repair` or
auto-repair commit (its SHA is in the KB's `git log`, reason "auto-repair (background)") as a new commit through Meridian's
ordinary write path, and refuses any other commit.

The commits of `--apply` and `--revert` are authored as whoever runs the command (D904), by the
bridge's rule: the KB's `knowledge_bases.<kb>.identity`, else `knowledge_bases.default.identity`, else what git resolves in the
folder the command runs in, and only inside a work tree. An empty entry sends no author, an invalid
one sends none with a warning; without an author Meridian authors the commit as the token. A
scheduled run outside any repository is therefore authored as its token unless `knowledge_bases.<kb>.identity` or `knowledge_bases.default.identity` names
someone.

Exit codes: `0` nothing left to do, `3` only judgement work or checks that are not safe to run unattended remain,
`2` error. `--json` prints the report as an object (`checks`, `review_total`, `review_by_kind`,
`remaining`). Not to be confused with `cartographer doctor`, which checks Compass's provisioning.

### `cartographer kb secrets init <kb> --recipient <age-pub> [--key-file <path>] [--force] [--data <dir>] [--config <path>] [--local]`

Enables secrets on a KB with one command (#1911). The creation rule of the root `.sops.yaml` (`secrets/.*\.sops\.yaml$`, one age recipient) is written through the running server by the admin-only `secrets_init` tool and committed in the normal write flow; the key is installed here, on the server machine, as `<credentials_dir>/<kb>.age` with mode `0600`, because `--key-file` is a path of the operator's machine and no MCP call writes a credential (D2039). Like `kb reauthor` it is local (`--data`, `--config`, `--local`, D177) and needs a token that is an administrator's. **It needs no restart**: the server resolves the key on each read, so `kb_status` `capabilities.secrets` reads `enabled` on the next call.

Everything that needs no write is checked first: the recipient has the shape of an age public key, `--key-file` is an age identity (size capped, never printed) that holds the recipient, and the target `<kb>.age` is a regular file. A different existing rule or key is refused without `--force`, before anything is written; the same rule or key is a no-op (`unchanged`). `--force` replaces the whole `.sops.yaml` and the key file but does **not** re-encrypt the files already under `secrets/`: the output says to run `sops updatekeys` on them. Without `--key-file` only the rule is written, and the output names the file still to add. The command is idempotent: if the key install fails after the rule is committed, run it again. Output is one line per effect (`rule: created|unchanged|replaced`, `key: installed|unchanged|replaced|not given`). Exit `0` done or unchanged, `1` refusal or failure, `2` usage.

### `cartographer kb reauthor <kb> [--apply] [--json] [--data <dir>] [--config <path>]`

Recovery for a push a remote refused because of who authored or committed its commits (a membership
push rule, a private-email rule; D1063). It re-authors, under the identity the server file configures
for the KB (`git.identities`, the rule `serve` applies at mount), the commits that exist
only in the local clone, then pushes. The preview runs without an identity configured, lists the commits it would rewrite and names the
key to set (`identity_missing` in `--json`); `--apply` refuses until one is set: rewriting with the
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

### `cartographer kb attach <kb> --remote <url> [--data <dir>] [--config <path>]`

Gives a KB created with `--no-remote` (D134) the origin it lacks: it adds `origin`, checks the remote (fetching without tags) and pushes the KB's current branch with `git push -u`, never forced. Like `kb reauthor` it is offline and local (`--data`, `--config`, `--local`, D177) and takes the KB's advisory process lock, so a running service does not have to be stopped, **and needs no restart**: the remote of a KB lives only in the git config of its clone, nothing is written to the server file, and the server reads it at its next sync decision, so the KB syncs on its next read or write. `sync_status` and `kb_status.push_state` stop saying `no_remote` as soon as the clone has an `origin`.

Accepted remotes: an **empty** repository, or one whose only branch is the KB's current branch and whose tip is an ancestor of the local `HEAD` (or equal to it), so the push is a fast-forward. Refused before any push, with the reason: other branches present; a different branch name; a tip that is not an ancestor (diverged or ahead of the KB); tags only; a forge's initial commit (README, LICENSE, `.gitignore`), which `kb create` adopts by building a new KB on top of it and which cannot be done to a KB that already has history (empty the repository, or `kb clone` it if it holds a KB). There is no `--force`.

A KB that already has an `origin`: the same URL is a no-op (exit `0`, no network); another URL is refused, since changing or removing a remote is a `git remote` operation done by hand. The three network calls use the KB's own credentials (`<kb>.ssh`/`default.ssh`, `<kb>.token`/`default.token` of the credentials directory, D905), so the check proves what the server will do; with none it is the ambient git behaviour of `kb create`. A URL that carries a password (`https://user:token@host/...`), a user name on an http(s) URL (where a token goes), or that cannot be parsed is refused with exit `2`: it would be written to `.git/config` (`kb create --remote` refuses the same URLs the same way, before it writes anything, #1851) (`ssh://git@host/...` and `git@host:path` are fine); use a credential helper or the credentials directory. The URL is printed redacted everywhere.

**Atomic on failure**: any refusal or failure (unreachable remote, rejected push) removes the `origin` it added, so the KB is exactly as it was and the server does not push by itself on its next write. A push refused for the commits' author hints at `kb reauthor`. Exit `2` is usage and validation, `1` a refusal or failure, `0` attached or already attached.

### `cartographer service sync-timer <action>`

The scheduled sync trigger (D140) for clients with no session-start hook, or one that fires only in
some sessions (Kiro, D300). With `auth: true` it authenticates from the token file `connect` wrote
(D699), since a timer inherits no shell exports; the units and the cron entry set `PATH` themselves (the binary's directory, the installing shell's, the package managers'). It is distinct from the
**server** service below, with its own unit files:

```bash
cartographer service sync-timer install [--interval 30m]
cartographer service sync-timer uninstall
cartographer service sync-timer status   # exit: 0 active, 3 installed but inactive, 4 not installed
```

| Platform | Files | Logs |
|---|---|---|
| macOS | `~/Library/LaunchAgents/com.cartographer.sync.plist` | `~/Library/Logs/cartographer/sync.log` |
| Linux (systemd) | `~/.config/systemd/user/cartographer-sync.{service,timer}` | journal (`journalctl --user -u cartographer-sync`) |
| Linux (cron, no systemd) | block `sync` of the user crontab, definition `~/.config/cartographer/cron/sync.cron` | `$XDG_STATE_HOME/cartographer/sync.log` (default `~/.local/state/cartographer`, via `sync --log-file`) |
| Windows | `%LOCALAPPDATA%\cartographer\tasks\sync.xml` → Scheduled Task `\Cartographer\Sync` | `%LOCALAPPDATA%\cartographer\Logs\sync.log` (via `sync --log-file`) |

`install` is idempotent (it overwrites and re-registers); uninstalling a timer that is not
installed is a success. On cron the interval is rounded up to whole minutes, and from an hour up to
whole hours capped at daily (`*/n` fires at :00, :n, ... of each hour, not n minutes after the last
run); uninstall strips the block and keeps `sync.cron` if the crontab still holds it. The per-user scheduler belongs to the account, not to `$HOME`: when
`$HOME` is not the account's home directory (a sandboxed run, the e2e suite), the Meridian service
and the sync timer write their unit files under `$HOME` and register nothing,
so a sandbox never replaces the account's own jobs (#683); `connect` and `setup` then print `sync timer not installed: HOME is not this account's home (expected in tests)` as a plain line, not a warning. The timer runs plain `cartographer sync`: `sync` has no trust flag to give it (D906; `workspace bind codex --trust` is a separate, explicit command),
and an MCP descriptor still needs the local approval an unattended job cannot give. `setup` and `connect` install the timer themselves when a connected provider has no
session hook, or one limited to some sessions (D325); `setup` lists it in its plan. `uninstall` is
the explicit opt-out, remembered as `sync_timer_opt_out` in the machine file (so `connect` does
not reinstall it, and `doctor`'s `trigger` check is info rather than a warning); `install` clears
it. `disconnect` removes the timer when no remaining client needs it, without an opt-out. `status`
only names the command.

On Windows the trigger is a repetition at the configured interval with *start-when-available*
(systemd's `Persistent=true` analogue: a run missed while the machine was off happens as soon as it
is on), and the interval reported by `status` is read back out of the definition on disk, so there
is no second source of truth. The log file is what makes a failing background sync diagnosable
there: a task's own history records exit codes only, and is disabled by default on many machines
([D217](decisions/D217-the-native-service-on-windows-is-a-per-user-scheduled-task.md)).

### Headless hosts, CI and agent containers

`connect` and `sync` run fully non-interactively; no flag is missing for an unattended host. Compass is not a workload: the binary runs next to the agent and writes under that agent's `$HOME`. The two scenarios below are listed in [deployment](deployment.md) §Deployment scenarios.

**Non-interactive `connect`.**

```bash
cartographer connect claude --no-input \
  --server-url https://kb.example.com --auth --token-env CARTOGRAPHER_TOKENS --kb all
```

- A provider named positionally forces the non-interactive path. With none named and no agent detected the command exits 1 and writes nothing.
- A server that mounts two or more KBs needs `--kb` on the first connect. `--kb all` reads Meridian's KB list, so a connect made where Meridian is unreachable (an image build) names the KBs instead.
- The token is the variable `--token-env` names. `connect` copies it to `~/.cartographer-token` (`0600`) so a run without the variable still authenticates, but **the agent's own process must export the same variable**, because the MCP entry references it.
- A plain `http://` URL on a non-loopback host is refused unless `--allow-insecure-http` (§Transport security).

**One-shot `sync`.** `cartographer sync` is idempotent, exits 0 on success, 2 when a KB failed (it wins over 3) and 3 when the symlink guard refused a write (an MCP entry or an artifact, as `connect`; one stderr line names provider and parts, which tells it from 3 as "client lock denied"; not on `--dry-run`; a refused hook registration or removal stays a warning; D2118), and checks the token variable or file before any network call. It has no daemon, watch or interval flag: periodicity comes from `service sync-timer`, from the agent's session-start hook where one exists, or from the host's own scheduler.

**Headless always-on host** (a VM, or a container with an init system). Install the binary with `install.sh` (`CARTOGRAPHER_INSTALL_DIR` for a path that is not root's; `CARTOGRAPHER_INSTALL_TAG` pins the tag of the Server and avoids version skew), run `connect` as above, then `cartographer service sync-timer install --interval 30m`. On Linux that is a systemd **user** timer, so the host also needs `loginctl enable-linger <user>` to survive logout, as for the Server's user service. Where there is no user manager, a scheduler or a loop runs `cartographer sync`.

**CI, a devcontainer or an agent pod.** The binary goes **in the agent's image**: `COPY --from=ghcr.io/beppetemp/cartographer:<tag> /usr/local/bin/cartographer /usr/local/bin/cartographer` (a static build), or `install.sh`. No client image is built. Run `connect` at container start, or at build time with explicit `--kb` names; run one `sync` before the agent starts; keep `$HOME` writable; take the token from the platform's secret as an environment variable; set `CARTOGRAPHER_NO_UPDATE_CHECK=1`; and point `--server-url` at the HTTPS URL of the Server (for an agent pod, the Ingress URL).

**What the recipes leave to a person.** A KB's hooks, executable files and MCP descriptors stay `needs_approval` on an unattended client until someone runs `cartographer approve` (§Approval of MCP descriptors, hooks and executable files). The recipes never pass `--yes` for it.

### `cartographer service <action>`

Manages the **server** as a native user service on the machine (local mode, D73):
launchd on macOS, systemd user unit on Linux, a per-user Scheduled Task on Windows
([D217](decisions/D217-the-native-service-on-windows-is-a-per-user-scheduled-task.md)). None of the
three needs administrator rights. Client and server are the same binary: the Compass subcommands
talk to the daemonized server over loopback.

```bash
cartographer service install [--config <path>] [--data <dir>] [--http <addr>] [--no-tray] [--config-only]
cartographer service install --wsl [<distro>]   # Windows: the server runs in that WSL distro
cartographer service uninstall|start|stop|restart
cartographer service tray install|uninstall|status   # the status icon's autostart (D1482)
cartographer service restart --wait [--config <path>]   # graceful, version-gated (D121)
cartographer service status        # exit: 0 running, 3 installed but stopped, 4 not installed
```

Every service definition (launchd plist, systemd unit, cron definition) is written and removed through `internal/safewrite` (D148): a symlinked destination or parent directory is refused with an error naming the path, and uninstall removes only a regular file.

`--config-only` writes the config and creates the data directory exactly as `install` does and installs nothing: for a host with no service manager, where plain `install` exits 2 before writing anything and prints the manual route (see `deployment.md`, "Hosts without a service manager"). It prints the `serve` command to start by hand and exits 0. `connect` does not offer to install the service on such a host.

**Windows with WSL** (`deployment.md` §Windows with WSL). In a WSL2 distro `service install` also registers the Windows task `\Cartographer\WslKeepAlive` (S4U, else an interactive token with `wslg.exe`; a warning naming the `.wslconfig` alternative when neither, or interop, is available) and, on systemd, runs `loginctl enable-linger`; `service uninstall` removes the task. On Windows, `service install --wsl <distro>` (a bare `--wsl`: the single running distro) runs the install inside the distro and writes `%LOCALAPPDATA%\cartographer\wsl-server.json`; while it exists, `install`, `uninstall`, `start`, `stop`, `restart` and `status` run `wsl.exe -d <distro> -- cartographer service <verb> <flags>` with the terminal attached and exit with its code (`status --json` passes through), and a delegated `uninstall` removes it. `sync-timer`, `tray` and `ensure` are never delegated. Refused before delegating: a Windows path in `--config`/`--data` (they are paths inside WSL), a registered native `\Cartographer\Serve` task (both removal routes are printed), a distro without cartographer (the `install.sh` command is printed; exit 2). A version different from `cartographer.exe`'s is a warning. No marker, no delegation.

`cartographer wsl keepalive` (internal, not in the usage text) is what the keep-alive task runs inside the distro: it refuses outside WSL (exit 2), blocks until SIGTERM/SIGHUP/SIGINT and exits 0; on the cron backend it runs `service ensure`'s logic at start and every 60 s, logging an error to stderr and carrying on.

Re-running `service install` leaves the installed definition and the running process in agreement: on Linux a unit that was active is restarted after `enable --now` (which alone leaves a running unit alone), as launchd and the Scheduled Task already are. The launchd plists escape every path as XML, so `R&D` in a path is valid.

**Proxy and internal CA (#1966, [D1984](decisions/D1984-service-proxy-env.md)).** A service starts from a minimal environment, so `service install` records exactly `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` and `SSL_CERT_FILE` (and their lowercase forms) from the environment it runs in, and no other variable, in `service.env` beside the server config (`NAME=value` lines, mode 0600, written atomically, removed when none is set). `serve` reads it before anything else (the file beside its `--config`, or the one `--env-file <path>` names, which release-candidate definitions pass) and honours only those names, plus the `CARTOGRAPHER_SUPERVISED=1` marker the Windows install adds (a process started with its own marker keeps it); an unknown or malformed line is ignored and logged by its key, never its value. The launchd plist and the systemd unit also carry the values inline and are then written 0600; the Windows task and the cron backend carry no flag for it, so no credential is on a command line or in the task XML. A variable removed from the shell leaves the files at the next explicit `service install`; `upgrade-repair` and a binary switch keep what is recorded whatever their own shell has. `service status` prints an `env:` line with the recorded names, never the values; `doctor` does not report them. `service uninstall` removes the file.

**A definition the previous stable can start (#2094, [D2111](decisions/D2111-the-service-definition-stays-compatible-with-the-previous-stable.md)).** The Windows task runs `serve --config <config> --log-file <log>` and nothing more, as the previous stable wrote it; a flag that stable does not know makes a downgraded service exit 2. `upgrade-repair` re-renders a running service's definition when its arguments differ from this binary's render, not only when the program differs. After a downgrade from a release candidate, run `cartographer service install` with the installed binary.

An uninstall (Meridian, the sync and doctor timers, the tray) checks that the job is really gone before it deletes the definition: a Scheduled Task still registered, a launchd job still loaded or a systemd unit still enabled after the de-registration failed is an error, and the definition stays so the command can be run again. On Windows `service start` fails when `Enable-ScheduledTask` does, instead of reporting a start that did not happen. On Windows the three tasks (`Serve`, `Sync`, `Tray`) **belong to the user who installed them** ([D1979](decisions/D1979-windows-task-owner.md)): each definition names the user in `<Principal>` and carries an explicit descriptor granting that user's SID, SYSTEM and Administrators full control, so a normal session of the same user, such as an agent's terminal, can administer them. An install from an elevated shell is not refused: it registers the same user-owned tasks and prints one line saying so, which is also how a task an older install left owned by Administrators is handed back. `service install` registers the task first and stops the old server only after the scheduler accepted it. It then asks what holds the listen address (a task that is not `Running` is not waited on): a foreign program, or a Cartographer server the task did not start, is refused with the same two messages as the cron start, the task stays registered and is not started, and `service start` works once the address is free (#2092). `service restart` signals the shutdown event, waits for the old server to exit and starts the task; it runs `Enable-ScheduledTask` only when the task is `Disabled` (what `stop` leaves), so it needs only the right to start it. `upgrade-repair` re-registers each installed task (`Serve`, `Sync`, `Tray`) whose definition has no descriptor, without stopping anything; `connect`/`service tray install` also register the tray before stopping a running one; a change the scheduler refuses for lack of rights says whose task it is and which command to run once from an elevated terminal (`service install`, `service sync-timer install` or `service tray install`).

Plain `restart` restarts the unit and returns. `restart --wait` gracefully replaces the process
(`SIGTERM`, so in-flight requests drain) and only prints success once `/health` proves the
installed binary version is serving from a new process (its `started_at` differs from the one
read before the restart, D266); `--config` selects the config used for that verification,
and is otherwise unnecessary because the installed service definition is discoverable.

Windows takes the same three verbs to a different scheduler, so two behaviours are worth naming.
`stop` also **disables** the task, because its logon trigger and its restart-on-failure would
otherwise bring Meridian straight back; `start` and `restart` re-enable it first, which is why a
service stopped on purpose stays stopped and a restart after a stop still works. And the graceful
replacement is not a signal but a named event in the user's session, followed by an explicit
relaunch of the task — nothing else would restart a process that drained and exited cleanly.
`restart` and `uninstall` wait for the old server to exit before starting or unregistering the task,
and from another logon session (SSH), where the event cannot be reached, the task is stopped
instead and the command says so (D266).

`service install` also installs the **tray** (`cartographer tray`, below) so it starts at logon: a
LaunchAgent `com.cartographer.tray` on macOS, a Scheduled Task `\Cartographer\Tray` on Windows, started
at once so the icon appears without logging off; Linux has none. A failure to register it (an SSH
session has no GUI) is a warning, never an install failure. `--no-tray` or `service tray uninstall`
opts out and the choice is remembered (`tray_opt_out` in the machine file), so `upgrade-repair` does
not bring it back; `service tray install` clears it. `service uninstall` removes the tray with the
server, unless the client config has a connected agent: the tray then shows that client's server, so it is kept and the command says `status icon kept: …` (`service tray uninstall` removes it). `upgrade-repair` re-renders and restarts the tray (also when the service is stopped, and for
installs that predate it) so a tray running the previous binary picks up the new one; a tray failure
there is a warning and never changes the exit code. With no local service installed `upgrade-repair` refreshes a tray that is already installed (a client-only machine's tray runs the previous binary after an in-place update) and never creates one. `service tray install` with no service and no client config prints `no server to show yet: …`. `service tray status` exits 0 when installed, 4 when not. `--no-tray` over an installed tray also removes it. On Windows, `service tray uninstall`
and `service install` stop a running tray and wait for it to exit (they first post `WM_CLOSE` to its window so the shell drops the notification icon, then use `Stop-ScheduledTask` if no window answers or the process stays); one that survives is an error and
`tray.xml` is kept so the command can be retried. On Linux `service tray install` fails ("supported on Windows
and macOS only"), `uninstall` only records the opt-out and `status` exits 4.

Operational details (generated paths, defaults, behavior with an existing config, automatic
repair on `install.sh update` and Cask upgrade) in `deployment.md` §Example: native local service
and §Upgrades, schema migration, and repo growth.

### `cartographer tray [--log-file <path>] [--interval <duration>] [--config <path>]`

A status icon for the local server or, on a machine with no local service, for the server the client is connected to, in its own process (not part of `serve`, so it stays visible
when the server is down and can restart it). Windows (notification area) and macOS (menu bar) only;
on Linux it prints an error and exits 2. `cartographer service install` registers it to start at logon
(next section); a hand-typed `cartographer tray` works too. On Windows 11 a new notification-area icon
sits in the overflow (^) menu until the user pins it to the taskbar.

**Which server (D1862).** On every tick the tray looks at the installed service definition. A local service installed → the local server, as described below. None → the server of the client config (`server_url`; the client config is read on every tick, so a `connect` to another server is followed). No client config file either → the local path unchanged (a server run by hand shows Running, nothing answering is "Not installed"). Never both. An explicit `--config` forces the local server whatever the client config says; `--config` applies to the local server only. A client config that cannot be parsed keeps the last good server, or shows unreachable, and the error is logged once.

**Client mode** probes `/health` of `server_url` with that URL's scheme and the client's rules (`client.CheckServerURL`, `allow_insecure_http` / `CARTOGRAPHER_ALLOW_INSECURE_HTTP`; no forced `http://`, no loopback rewrite) and the client token, resolved as `sync` does (the literal, the `${NAME}` variable, then the private token file `connect` wrote, which is what a tray started by launchd or the Task Scheduler can read). The token is sent in the `Authorization` header of a second `/health` request only, after the anonymous one answered like a Cartographer server, never shown or logged; with `auth: true` and no token the probe is anonymous, so the KB list is absent and the menu says `Token not available`. The status line names the server host (`example.com 1.2.3 — 2 KBs`, `example.com: unreachable`, `example.com: starting…`) and the header badge reads Unreachable instead of Stopped. A 200 whose `/health` is not a Cartographer server's (no `version`) is Stopped with `another program answers on <host>` and the badge `Port in use`, as in the local mode (#1998). The menu is read-only: the header, update and Agents rows, the KBs, Open Atlas (on `<server_url without /mcp>/ui/`; always offered, since `/health` does not say whether the server serves the Atlas) and Quit. Restart Service and Open Log are not shown. Open Atlas and the KB entries accept `https://` and a reverse-proxy path prefix (a path ending in `/ui/`, no `.`/`..` segment, no userinfo or fragment).

A server that reports KBs with a failed or stale push adds `N KBs not pushed` to the tooltip and a `Commits not pushed` row (busy tone) to the menu: the count only, the KB names and remedies are in `cartographer status`.

Before the first probe the tooltip reads `checking…`, not `stopped`. An address where a program answers that is not a Cartographer server (a 200 without the `version` of its `/health`, a body that is not JSON, any other status) is Stopped, never Running: the tooltip reads `another program answers on <addr>` and the header badge `Port in use` (#1998). The tray reads the local server's `/health` every `--interval` (default 5 s, minimum 1 s), taking the
address from the installed service definition on every tick (`--config` overrides the config path used
only to read that address and the Atlas URL; Restart service calls the manager without it). A tick reads the definition file's presence and the config and probes `/health` once: it runs no scheduler command (`launchctl`, `systemctl`, PowerShell). For the
Agents rows it also runs what `cartographer status` runs, off the poll loop: it reads Compass
config, the lockfile and the agents' files and asks the Meridian it is configured for (authenticated, like
`status`), which may refresh the repository-path cache as `status` does. It never writes server or KB
state; it acts only through `service restart`'s code path and the system "open". The icon is the brand mark (Coordinate; the micro variant, without
the inner arc, below 32 px) and its **shape** tells the state, because a macOS template image is
tinted by the system: the mark = running (also a server that answers `ready: false` without bootstrapping, e.g. no KB
mounted: the menu badge reads "Not ready" and the line says `no KB mounted` or `not ready`, #1491), the mark without its point = starting (bootstrapping only), the mark crossed by a slash = stopped, the mark dimmed = service not installed.

Menu (D1495), top to bottom: "Cartographer" with the state at the right (Running, Not ready, Starting…, Restarting…,
Stopped, Not installed) over the version and the address; "Update available" with the newer
release, when `/health` reports one; an **Agents** section, one row per connected agent client with
"In sync", "Out of sync" or "Not checked" (anything `status` could not judge) at the right, or "None
connected"; read at start, every 5 minutes, whenever Meridian comes back and whenever the menu is about to be opened (macOS: the menu opening; Windows: the pointer reaching the icon or a click). Never two reads run at a time, and a request starts at most one read every 10 seconds (the 5 minute and server-came-back reads do not wait for that window). A request the window or a running read cannot serve is remembered and read when the window ends (at the next 5 s tick at the latest), so opening the menu, running `sync` and reopening it within 10 seconds still ends in the right row. A read that finishes is shown at once; a macOS menu that is open is redrawn only when the Agents rows changed (any other change waits for the next opening, so an open KB submenu is not closed under the pointer);
**Knowledge Bases (n)**, a submenu of the KBs `/health` lists, each opening the Atlas on that KB
(`/ui/?kb=<name>`); then Open Atlas (greyed when the config has no web UI), Restart Service (runs off
the UI thread, disabled while in flight and when no service is installed), Open Log, Quit (the tray
only; Meridian keeps running). On macOS the information rows are drawn in the label colour with the
states coloured (the brand's accent for Running and In sync, orange, red, blue for an update; fixed sRGB values per appearance in `internal/tray/tone.go`, each at least 4.5:1 against the light and the dark menu, pinned by a test) and the
actions have ⌘O, ⌘R, ⌘L, ⌘Q while the menu is open; Windows shows the same rows as plain text. `--log-file` appends the tray's own log there, as for `serve`. Either mouse button on the icon opens the menu on both platforms (a double-click has no action). A second tray exits silently with status 0: Windows by a named mutex, macOS by an `flock` on `~/Library/Application Support/cartographer/tray.lock`. A `Status` error that repeats is logged once, again when the message changes or after a success.

### `cartographer import`

A mechanical import scaffold (D74 WP2), a sibling of the agentic `cartographer-kb-import` skill
(`internal/skillbundle/bundled/cartographer-kb-import/`): unlike the other subcommands, it doesn't talk to the
server, it operates directly on a local clone of the KB (`--kb`). It walks the `.md` files under `--source`
(recursively, skipping hidden directories; a symbolic link and a file over 600 000 bytes are skipped with a line on stderr and counted as skipped), maps each source directory onto a destination map (or
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
| `--commit` | `false` | Makes one final commit containing only import-written paths; pre-existing dirty work is untouched. Its author and committer are the operator, the identity git resolves for the KB's folder, else Meridian (D904, D950) |
| `--message` | `import: <source> -> <kb>` | Commit message; implies `--commit` |
| `--dir-as-concept` | `false` | Promotes a source directory with `index.md` (or `README.md`) into an expanded concept and keeps its satellites together |
| `--overwrite` | `false` | Replaces a concept that already exists in the KB. Without it an existing concept (curated frontmatter, body, `status`) is left untouched, counted in `skipped` and named on stderr; `--dry-run` marks it |

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

**An existing concept is never replaced silently.** A second import after curation would otherwise
destroy the curation: the concept of the same id is skipped (stderr names it, the summary counts it as
skipped) unless `--overwrite`, and `--dry-run` opens the KB read-only to mark every collision. Source
names that slugify to the same id get distinct `-2`, `-3` suffixes even when one of them already reads
`foo-bar-2.md`; `.md` is recognised whatever its case (`Upper.MD` imports as `upper`, its links rewritten).

**`import` takes the KB's advisory lock** and fails fast when Meridian holds it (D155), naming the
holder and the `service stop … && service start` sequence: it writes into the same directory the
server's sync loop manages, and the two interleaving corrupted the git index. `--dry-run` writes
nothing and never contends for it.

**The search index is not updated by this process.** `import` writes through the KB write path but
from outside Meridian, so Meridian's FTS index knows nothing about it: until it is rebuilt `search` returns nothing
while `concept_list` sees everything. On completion the command rebuilds the index of **that KB only**, and only when the local server (the service config's `data:` or a `kbs[]` path) mounts the directory given to `--kb` and Compass points at a loopback server; for a clone Meridian does not serve, or a remote server, it prints the `cartographer reindex` instruction. It never changes the exit code: the import
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
in both cases it adds `status: imported` only when `status` is absent (an existing status is never
overwritten, so a file that already has one stays out of the `imported_draft` lint, a warning that
keeps the curation backlog visible across sessions). Relative markdown links `[text](path.md)`
are rewritten best-effort against the new layout; wiki-links `[[...]]` are left as-is
(D72). A source directory with neither `--map` nor `--default-map` fails the command
(no write) with the list of unmapped directories. Final output: counts of files
imported/skipped (non-markdown)/errors — a write error on a single file does not block the
rest of the batch. A concept that already exists is skipped, not an error (exit 0, D1589).

With `--dir-as-concept`, a directory containing `index.md` — or `README.md` when no
`index.md` exists — becomes `<map>/<directory>/`: the chosen file is written as that
expanded concept's `index.md`, while its sibling markdown files become satellites below it.
The dry-run labels the promotion explicitly. Without the flag, importing remains flat and a
source `index.md` is still rejected as a reserved destination filename. `--commit` also commits
the scaffold (`_map.md`, `index.md`, `log.md`) created for each new destination map; on partial
write failures it commits only successful paths and reports that the batch had errors.

### `cartographer surveyor source test --config <file> (--source <id> [--kb <kb>] | --recipe <name> --locator <loc> [--kind <source_kind>] [--include <glob>]...) [--cursor <c>] [--show <key>]`

The dry run of a Surveyor recipe: the listing the Surveyor will run, printed, with nothing written and no model called
(exit `0` listed, `1` the poll would record a `feed_error`, `2` could not start, `3` a system recipe's check
refused). Flags, output and limits are in [surveyor-recipes.md](surveyor-recipes.md#testing-a-recipe-before-deploying-it);
`cartographer surveyor --config <file>` itself is described in [deployment.md](deployment.md#the-surveyor-a-third-role).

### `cartographer resolve repo:<key>|path:<name>`

Resolves a path portability placeholder (D75) and prints the local path to stdout. It doesn't talk to the
server: it only reads `.cartographer.yaml` (`placeholders.search_roots`, `placeholders.paths`) and, if needed, scans the
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

A leading `~` is expanded in both `placeholders.search_roots` and `placeholders.paths` entries, followed by either
separator: `~/repos` and `~\repos` mean the same directory wherever the config is read. `~name` is
not expanded — another user's home is not something a config entry means.

A configured search root that does not exist, is not a directory, or cannot be listed produces a
**warning on stderr naming it and the OS error**, and the remaining roots are still scanned. It is a
warning, not a failure: a machine-local config may legitimately name a root that only exists on
another machine. The warning is separate from the depth message below, so a typo in a root is not
mistaken for a depth problem ([D216](decisions/D216-the-client-half-reaches-parity-on-windows.md)). The same warnings
surface in `AppliedResult.Warnings` when a `{{repo:…}}` placeholder is resolved during `sync`.

Exit code: `0` resolved (path on stdout), `1` not resolved (no `paths:` entry, no clone
found under `placeholders.search_roots`, or an ambiguous key across several distinct remotes — error message on
stderr with the full form to use), `2` usage error (missing argument or not in the
`repo:...`/`path:...` form).

### `cartographer config [--json] [--all]`

Shows every setting with its effective value and where it comes from (D743), in two groups: the Compass settings of
`.cartographer.yaml` (origin `file` when the key is written there, otherwise `default`) and, when the configured server
answers `GET /api/ui/v1/settings` with Compass's token, Meridian's: its keys, then one group per KB. One line per
key: `key  value  (origin)`, with `restart required` when the schema says so. By default only the keys something set are
listed; `--all` adds the rest with their default. A per-KB key the KB's own `settings.yaml` sets shows origin `kb`, which beats `file`, `env` and `flag` (D746). A secret shows `(set)` or `(unset)`. `--json` prints the raw
structure (`client`, `server`, `note`). A server that is unreachable, refuses the token or predates the route costs a
one-line note instead of the Meridian group, and the exit code stays 0. The same data is the Atlas Settings page
(`control-plane.md` §Read-only UI API).

The Atlas Settings page also shows Compass, as `sync` last reported it: after every apply, `sync` posts a report to
`POST /api/component/v1/settings` with `component: "client"` (`control-plane.md` §Client settings reports): the Compass schema keys with their origin, the
`.cartographer.yaml` as on disk, the connected agent clients (scope, entry kind, bound folders whatever the scope, and their
KBs) where each placeholder of the connected KBs resolves on this machine, and the installed plugins as `plugin list` shows them (name, version, state, digests, redacted source, description, contribution counts per point and the names of the declared permissions; D2079). No secret value, token or file content other
than that config file. Meridian keeps it in memory for the sending principal only. Best-effort: a server that is
unreachable, predates the route or refuses the token costs a debug line, never the sync (D818).

### `cartographer paths [list [--json]] | set <key> <path> | ignore <key> | unset <key>`

The placeholder keys the last sync met, and the command that records where they live on this
machine (D262). No network call: everything is read from the lockfile and `.cartographer.yaml`.

```bash
cartographer paths                          # = paths list: every key, its KB(s), path or failure reason
cartographer paths list --json              # {"placeholders": [{key, kbs, path | reason, description, default, ignored}]}
cartographer paths set path:kubeconfig ~/.kube/config
cartographer paths set repo:dotfiles ~/src/dotfiles   # answers `paths: repo:dotfiles -> <path>`: the echo carries the `<kind>:<key>` id
cartographer paths ignore repo:work-tools      # never exists on this machine: stop reporting it (D282)
cartographer paths unset kubeconfig           # remove a mapping, or an ignore
cartographer paths suggest                  # a candidate path, as a `paths set` line, for every unresolved key (D320)
cartographer paths --help                   # the usage above, on stdout, exit 0 (also `paths help`)
```

- `suggest` reads the same unresolved, not ignored keys and prints, per key, its KBs, its
  description and one `cartographer paths set <key> <path>` line per candidate — or a line saying
  there is none. Candidates, in order: the `paths.yaml` default, when it exists on this machine;
  for a `repo:` key, every clone under `placeholders.search_roots` whose origin remote contains the key name or
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
  and scan. A path that is absolute or starts with `~` is stored as written (`~` kept, expanded at resolution time); any other (`.`, `../dotfiles`) is made absolute against the current directory first, because the sync that resolves it runs from a hook or the timer in another directory. The command prints the path it registered. A path that does
  not exist is recorded anyway with a warning (you may be about to clone it); for a `repo:` key, a
  path that is not a git clone is a warning too, never an error.
- `ignore` marks a key as deliberately absent on this machine (D282), for a work repo on a personal
  laptop or a folder that exists on another host only. It is stored in `placeholders.ignored` in
  `.cartographer.yaml` with its kind prefix (`repo:work-tools`); an unprefixed argument is qualified
  from the lockfile and refused (exit `2`) when the lockfile does not know it. Resolution is
  untouched — the placeholder stays verbatim, and a key that does resolve still does — only the
  reporting changes: the sync warning, `status` and the connect step stop naming it, and `list` shows
  it as `IGNORED` (`"ignored": true` in the JSON) instead of counting it unresolved. A key mapped under
  `paths:` cannot be ignored (exit `1`), and `set` on an ignored key lifts the ignore.
- `unset` removes an entry or an ignore (exit `1` if there is neither). With an explicit kind (`unset repo:x`) only the ignore of that kind goes, the one of `path:x` stays; without a prefix both kinds go.
- `set`/`ignore`/`unset` print the one command that applies them — `cartographer sync` — and never sync
  implicitly. They need an existing `.cartographer.yaml` (exit `2`, run `connect` first).

Editing happens here, not in the sync summary.

### `cartographer plugin <subcommand>`

Installs, approves and manages plugins: a git repository or a folder with a `cartographer-plugin.yaml` manifest that
adds agent clients, skills, subagents, hooks, commands, MCP tools and sync handlers to this machine. Nothing runs
before the user approves the exact version (its digest). The full reference is [plugins.md](plugins.md); the
subcommands are:

| Subcommand | What it does |
|---|---|
| `plugin new <name> [<dir>]` | Scaffolds a schema 2 plugin |
| `plugin validate <dir> [--output table\|json]` | Checks a folder offline and lists every problem; prints what its scripts register. For a schema 2 plugin it also prints its sides, those this build cannot load and the entries it does not know (JSON: `sides`, `unsupported`, `unknown`, only under schema 2). `install`, `update` and `approve` refuse a schema 2 plugin without a loadable client side |
| `plugin install <git-url>[@<ref>] \| <folder> [--yes]` | Fetches, validates and stores it, prints the preview and asks before enabling; `--yes` is the consent |
| `plugin test <dir> [--recipe <name> \| --connector <name>] [--output table\|json]` | Runs the recipes and connectors of a plugin folder (its surveyor side) against `tests/<name>.yaml` and the conformance checks, with no server, token or model. A Starlark recipe's `http.request` is answered from recordings (offline); **an exec recipe's and a stdio connector's command run as the user, unapproved and with the user's network**, after one line per command is printed on stderr (an http connector is skipped, its calls checked against the allowlist). `--recipe` and `--connector` cannot be combined (exit 2). Exit 0 all passed, 1 a check failed, 2 it could not run. JSON: `schema` (`cartographer.plugin/v1`), `passed`, and `results[]` each `{target, check, status (pass\|fail\|skip), detail}`. See [plugins.md](plugins.md#testing-a-surveyor-side-plugin-test) |
| `plugin vendor <git-url>[@<ref>] \| <folder> --to <dir> [--side <side>]` | Stores a plugin in a directory a headless component reads read-only and prints its digest and the config lines that pin it; writes no approval |
| `plugin list`, `plugin show <name> [--files]` | The plugins and their state; the preview again, from the store |
| `plugin approve <name> [--yes]` | Approves the installed version after printing what runs; without a terminal it needs `--yes` |
| `plugin update [<name>] [--yes]` | Fetches again and prints the diff; the approved version stays active until the new one is approved |
| `plugin config <name> [key=value ...] [--unset <key>]` | Sets a setting the manifest declares; needs no approval and never widens a permission |
| `plugin enable <name>`, `plugin disable <name>` | Turns an approved plugin on or off |
| `plugin remove <name>` | Retires what it installed, then forgets it; works on a plugin whose `cartographer` range the running version does not meet |

An active plugin's commands run as `cartographer <plugin> <command> [args...]` (`cartographer <plugin>` lists them,
and `help` shows a `Plugins` group); its `sync.after` and `connect.after` handlers run after `sync` and `connect`,
never in `--dry-run` and never changing their exit code; its tools are served by the stdio bridge and by
`cartographer call`. The `plugins` key of `.cartographer.yaml` is written only by these commands.

## Adding a provider

Every supported provider is one descriptor in `internal/configurator/registry.go`
([D137](decisions/D137-declarative-provider-registry-two-tables-owned-by-the.md)): its `Provider` constant and wire value, display
name, native MCP config file and format (`FormatJSON` with its server key, or `FormatTOMLBlock`),
whether that file may be deleted once emptied (never for Claude Code — `.claude.json` is Claude's
own shared state), whether it can carry MCP auth headers, whether its MCP tool namespace is flat
across servers, the longest tool identifier it accepts (`ToolIdentifierLimit`, which `connect`
and `sync` check), the agent client majors whose hook plugin shape is known (`HookPluginMajors`, which
`doctor` checks), the global instructions files that shadow the managed one
(`InstructionsPrecedence`, which `doctor` checks), the detection evidence (binary names, config directories in probe order, optional
env-anchored config directories, optional per-GOOS application directories), and its emitter
function.

Two orders are exposed and both are user-visible: `Providers()` — the order `EmitAll` and the
client subcommands iterate — and `DetectionOrder()`, the order `cartographer agents`
lists agents in.

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
| Claude Code | `.claude.json` (also `<dir>/.claude.json` for each `providers.claude.config_dirs`, D755) | `mcpServers` (JSON) |
| Codex CLI | `.codex/config.toml` | managed block `[mcp_servers.cartographer]` (TOML, marker `cartographer:mcp:*`) |
| Kiro | `.kiro/settings/mcp.json` | `mcpServers` (JSON) |
| OpenCode | `.config/opencode/opencode.json` (an entry left in `opencode.json` by an earlier version is moved, #1172) | `mcp.servers.<name>` (2.x; the V1 `mcp.<name>` on 1.x; an unreadable version keeps the shape already on disk, and `sync` does not remove an entry it re-emits, #2102, so that holds) |
| Google Antigravity | `.gemini/config/mcp_config.json` | `mcpServers` (JSON) |
| Crush | `.config/crush/crush.json` | `mcp` (JSON) |
| GitHub Copilot (CLI, VS Code) | `.copilot/mcp-config.json` | `mcpServers` (JSON) |
| Pi | `.pi/agent/mcp.json` | `mcpServers` (JSON) |
| Cline | `.cline/data/settings/cline_mcp_settings.json` | `mcpServers` (JSON) |

KB-provided stdio descriptors (D116) share these same files with per-name ownership. Claude Code,
Codex and Kiro receive native `command`, `args` and `env` fields (Kiro also keeps `autoApprove: []`);
OpenCode uses `type: "local"`, an ordered command array and `environment` with `{env:VAR}` references,
under `mcp.servers` (D2056);
Crush uses `type: "stdio"` with a string `command` plus a separate `args` array.
Copilot (CLI and VS Code) uses `type: "stdio"` with `command`, `args` and `env`, and every entry carries `"tools": ["*"]`.
Pi has no `type` key (it infers the transport); Cartographer's own entry carries `toolExposure` for the entry tools and no `exposure` (below).
Cline uses `type: "stdio"` (or `"streamableHttp"`) and never gets a `cwd`, `disabled`, `autoApprove` or `timeout`.
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

**OpenCode** (schema: https://opencode.ai/config.json) — 2.x, the shape `opencode mcp add`
writes and the client reads natively (D2056):
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "cartographer": {
        "type": "remote",
        "url": "http://127.0.0.1:39273/mcp"
      }
    }
  }
}
```

**OpenCode** — with auth (OpenCode's native `{env:VAR}` syntax; the header disables OAuth):
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "cartographer": {
        "type": "remote",
        "url": "http://127.0.0.1:39273/mcp",
        "headers": { "Authorization": "Bearer {env:CARTOGRAPHER_TOKENS}" },
        "oauth": false
      }
    }
  }
}
```

**OpenCode 1.x** gets the V1 flat map instead, `mcp.<name>` with `"enabled": true`; 2.x still
normalizes it on load, so a file an older Cartographer wrote keeps working until the next `sync`
converges it on the native shape above.
When the installed version cannot be read (`opencode` off `PATH` in a timer `sync`, a probe
timeout) the shape already on disk for that server is kept: a flat `mcp.<name>` entry stays flat
(rewritten with `enabled`, no `oauth`), a native one stays native, and neither writes the native
default. A merge that would replace what the user wrote is refused instead, with
`ErrMCPSectionUnusable` and the file untouched: `mcp` or `mcp.servers` that is not an object, or
`mcp.servers` that is a V1 server entry (a server named `servers`). `connect`/`sync` report it, a KB
descriptor is skipped with a warning.

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
error naming the Cartographer entry and the key, instead of being written for Crush to execute on its next start.
Crush has no documented user-level subagent directory, so that artifact kind is unsupported for it.
Its hooks (`hooks.PreToolUse` in `crush.json`, D363) are registered by name; only `PreToolUse` fires,
so there is no session event and its bootstrap trigger is the scheduled timer.

**GitHub Copilot (CLI and VS Code)** (`copilot`, D676, [D1864](decisions/D1864-copilot-vscode-client.md); probed on CLI 1.0.94 and VS Code 1.145.2) — with auth. The file is
`~/.copilot/mcp-config.json`; Copilot expands `${VAR}` in a header value from its environment, so the
header is written as for Claude Code. `"tools": ["*"]` is required: without it a non-interactive
session is offered no tool of the Cartographer server:
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
`$HOME/.copilot` like every other provider and ignores `COPILOT_HOME`: both clients honour it, so a user who
exports it gets cells the client does not read.

VS Code's **Copilot harness** (the chat runs the embedded Copilot CLI runtime) reads the same
`$HOME/.copilot` cells, so one provider covers both. Detection is the `copilot` binary or `$HOME/.copilot`;
VS Code creates that directory when Copilot is first used, so a VS Code install whose Copilot never ran is
not detected, and neither the app bundle nor `code` on PATH is evidence. VS Code's **Local** harness is not
written to and Cartographer never writes or reads the VS Code profile folder (`Code/User/…`). VS Code also
reads `.github/agents` and `.github/hooks` in a trusted workspace, and a workspace bound to the provider
gets `.github/agents/<name>.agent.md` and `.github/hooks/cartographer-<name>.json` (the hook's files in
`.github/cartographer-hooks/<name>/`), observed in a trusted workspace only; the project-local MCP location
is unsupported (VS Code does not load `.mcp.json` with default settings,
[D676](decisions/D676-github-copilot-cli-is-a-provider.md)).

**Pi** (`pi`, npm `@earendil-works/pi-coding-agent`; probed on 1.5.0) — with auth. The file is
`~/.pi/agent/mcp.json`, key `mcpServers`. Pi interpolates `${VAR}` in `env` and `headers`, so the
header is written verbatim, as for Claude Code. Pi's default exposure, `codemode`, does not declare a
server's tools to the model, and Cartographer does not switch it off: its own entry pins only the entry
tools (`atlas_overview`, `search`, `concept_read`, `graph_context`, the reading path) with
`toolExposure`, so they are declared like built-ins and every other tool stays reachable from codemode
([D1724](decisions/D1724-entry-tools.md), amending D1603). A third-party server of a KB `mcp`
artifact keeps `"exposure": "direct"`:
```json
{
  "mcpServers": {
    "cartographer": {
      "url": "http://127.0.0.1:39273/mcp",
      "headers": { "Authorization": "Bearer ${CARTOGRAPHER_TOKENS}" },
      "toolExposure": {
        "atlas_overview": "direct",
        "search": "direct",
        "concept_read": "direct",
        "graph_context": "direct"
      }
    }
  }
}
```

Pi's MCP tools are named `mcp__<server>__<tool>`, as in Claude Code. Its global cells are
`~/.pi/agent/mcp.json`, `~/.pi/agent/AGENTS.md` (a managed block; an `AGENTS.override.md` beside it
replaces it and is reported as shadowing) and `~/.pi/agent/skills/<name>/`; it has no subagents and its
hooks are a generated extension per hook, `~/.pi/agent/extensions/cartographer-<name>.ts`, with the hook's own files in `~/.pi/agent/cartographer-hooks/<name>/` (`SessionStart`, `PreToolUse`, `PostToolUse`; `docs/sync.md` §Agents and hooks). The only project cell is `AGENTS.md` (read without trust): project `.pi/*` and
`.agents/skills` load only once the project is trusted. A value that is wholly `!command` in `env` or
`headers` is refused (Pi would run it when it loads the file), and so is a server name outside letters,
digits, `_` and `-` (Pi skips it silently). Cartographer reads `$HOME/.pi/agent` like every other
provider and ignores `PI_CODING_AGENT_DIR`: to use another agent directory, symlink `~/.pi/agent`.
A `pi` on `PATH` is reported as installed only when `pi --version` prints a bare version
([D676](decisions/D676-github-copilot-cli-is-a-provider.md), [D755](decisions/D755-claude-config-dirs-declared-not-discovered.md)).

**Cline** (the VS Code extension `saoudrizwan.claude-dev` and the `cline` CLI; read from the extension bundle 4.1.26) — the bridge only. The file is
`~/.cline/data/settings/cline_mcp_settings.json`, key `mcpServers`, which Cline owns (it keeps a `.bak` beside it and
recreates it): other servers and keys survive, and the file is never deleted. Cline declares no MCP roots and starts the
bridge in the extension host's folder, so every session is unbound and gets the provider-default access; a user who wants
one folder sets `cwd` by hand, Cartographer never writes it. Its global cells are the MCP file,
`~/.cline/rules/cartographer.md` (the managed block, a file Cartographer owns whole: Cline reads every `.md` there as an
always-on global rule), `~/.cline/skills/<name>/` and `~/.cline/agents/<name>.yaml` (a translated subagent; `tools` and
`model` are dropped and an empty prompt falls back to the description, since Cline skips an agent without one). Hooks are `~/.cline/cartographer-hooks/<name>/` plus an owned dispatcher
per event in `Documents/Cline/Hooks` (`TaskStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`), which calls the internal
`cartographer hook dispatch --provider cline --event <Event>`: it reads Cline's payload, runs the Cartographer hooks of the event
and answers `{cancel, contextModification, errorMessage}` as one line of ASCII JSON, always with exit 0 (a `PreToolUse` block stops
the whole Cline task). Every project cell is unsupported. `CLINE_DIR` is ignored like every provider-specific home override. Detection is the
`cline` binary, `~/.cline`, or the extension directory `saoudrizwan.claude-dev-*` under an editor's `extensions` folder.

The formats above are generated from the same provider-neutral core,
`configurator.EmitServer(name, spec ServerSpec, provider)` (D69): `Emit(cfg, provider)` is a
thin wrapper around `EmitServer(cfg.Name, cfg.toSpec(), provider)`. The same `EmitServer` is
reused by `internal/provisioning` to materialize the third-party MCP servers a KB
distributes (`mcp/<name>.json`, kind `mcp`) — not Cartographer's own entry, but any
server, with per-name ownership in the same file (`mcpServers.<name>`/`mcp.servers.<name>`/block
`[mcp_servers.<name>]` marked `# cartographer:mcp:<name>:begin/end`). Details →
`docs/sync.md` §MCP servers.

## `.cartographer.yaml`

### Approval of MCP descriptors, hooks and executable files

Every MCP descriptor a KB ships, **signed or not**, needs a separate, local approval: an MCP server
receives the agent's data, a signature proves only which server a descriptor came from, and since D906 any
`rw` token on the KB can write a descriptor Meridian then signs. A KB hook, any KB artifact that ships
a file with the execute bit, and a skill or agent whose frontmatter declares `hooks` or MCP servers (also in a
`providers:` block) need the same approval because they run on this machine (D1141), signed or not. Nothing else Compass installs
waits for a decision (`sync` has no `trust` setting and no `--auto-trust`; the one explicit trust write is `workspace bind codex … --trust` for Codex project trust, D1074), and bundled artifacts never do.
Inspect and record the current content with `cartographer approve <kind> <name> --kb <kb> --yes`
(`kind` is `mcp`, `hook`, `skill` or `agent`; interactive terminals default to no confirmation; for a
non-MCP kind the command lists the files and prints the `hook.json` and the content of the executable files). Approval records the source KB,
artifact name (`<kind>/<name>` for everything but an MCP descriptor), content hash and timestamp in
`.cartographer.yaml`; a content change requires reapproval. `cartographer approve revoke <kind> <name> --kb <kb>`
takes effect on the next `cartographer sync` (revoking an approval that was never given is an error, exit 2, and without `--kb` it says whether there is nothing to revoke or the name is approved from several KBs): for an MCP descriptor it prunes the managed
provider config; for a hook or an executable file it stops refreshing what is installed, which is left in
place, registered and running (the command says how to remove it). A hook or executable file that is waiting is reported as pending on every sync and is not
refreshed, registered anew or removed until approved.

`cartographer approve --pending [--kb <kb>] [--yes]` approves every hook, skill or agent `sync` would report as
`needs_approval` after one confirmation, and before it prints each of them exactly as the single form does
(the files with their size and execute bit, the `hook.json`, the content of every executable file and of a
frontmatter that declares hooks or MCP servers), so the confirmation covers what will run. An MCP descriptor is
never approved this way: it is named with its own `cartographer approve mcp <name> --kb <kb>` command. Without a
terminal it needs `--yes`, like the single form. Names, paths and targets chosen by Meridian are printed with
every non-printable character escaped (`\n`, `\x1b`, `\u202e`), so a name cannot forge a line, rewrite the screen or read as another command. Then run
`cartographer sync`.

For an MCP descriptor that was approved before, the preview also prints a diff of the changed lines against the approved text (`unchanged since approved on <date>` when the hash is the same). Every form of `approve` and `approve revoke` takes `--sync`: it runs `cartographer sync` after a successful write and exits with its code; a declined or failed approval never syncs.

**One-time migration (D1166).** The first `sync` of a client configuration that has never run it approves,
by itself, a hook, skill or agent that needs approval, has no approval recorded and is already installed
with the same content hash the KB serves now (the lockfile entry says so), and prints one line per artifact:
`approved <kind>/<name> [kb:<kb>]: installed before approvals existed, unchanged`. An artifact whose hash differs
from its lock entry, or that is not in the lockfile, stays `needs_approval`. The migration runs once: its
marker, `approvals_migrated`, lives in the machine file `.cartographer-machine.json` beside the approvals, and stays in `.cartographer.yaml` as a tombstone (so losing the state file or the machine file cannot re-arm it), is never cleared, and is not set by a dry run or
by a sync that could not pull every KB, so a later `approve revoke` is never undone. A `sync -dry-run` before it
previews it: `would approve <kind>/<name> [kb:<kb>]: installed before approvals existed, unchanged` for what the real
sync would approve, `needs_approval` only for the rest, and the marker stays untouched (#1176). A first `connect` of a client with no agent and no install yet writes the marker itself, so its first sync does not rewrite `.cartographer.yaml` (#1302); a client that is already connected keeps it unset until its own first sync.

Written/updated by `connect` in the user's home directory (`~/.cartographer.yaml`, machine-wide —
`clientconfig.TargetDir`, D52): it records which server the machine is connected to (the connected providers are in the Compass state file, below). One file per machine, not per project: this avoids drift with a provider
connected in one repo but not another.

```yaml
server:                         # what the machine needs to reach the server (D1731)
  url: http://127.0.0.1:39273/mcp
  name: cartographer            # name under which the server is registered in the MCP configs (always "cartographer", overridden only by editing this file)
  auth: false
  token: ${CARTOGRAPHER_TOKENS} # a literal token or ${ENV}; a literal keeps this file at mode 0600 (D906). Unattended runs fall back to the private token file
  allow_insecure_http: false    # D1023; written only when true. true: accept a plain http:// server.url that is not loopback (the token then travels in clear); set by `connect --allow-insecure-http`
defaults:
  unbound_access: read          # access of a folder under no workspace binding, for every provider (read | write); a provider's own unbound_access beats it
providers:                      # one entry per provider (D1731); a provider with no entry receives every known KB
  claude:
    kbs: [homelab]              # per-provider KB binding (D169); absent = every known KB
    scope: workspace            # provider | workspace (D193); set by `cartographer workspace bind`
    unbound_access: write       # D1731; this provider's mode for an unbound folder
    config_dirs: [~/.claude/accounts/b]   # extra Claude Code config dirs (CLAUDE_CONFIG_DIR) whose .claude.json also gets the MCP entry (D755); claude only; does not make the binding explicit
    access: read                # with tools: minimal, the read-min profile of `connect --access read-min` (D1184)
    tools: minimal
    workspaces:                 # explicit workspace bindings, [{path, kbs, worktrees}] (D193); set by `cartographer workspace bind`
      - path: ~/projects/work
        kbs: [homelab]
        worktrees: true         # `workspace bind --worktrees`: opt in to the repository's linked git worktrees; absent = off, a rebind without the flag turns it off
  opencode:
    allow_symlinks: [$HOME/.config/opencode]   # links that writes may follow, the link itself (D148 refined); off by default; edit with `cartographer client symlinks`; does not make the binding explicit
knowledge_bases:                # one entry per KB, for every provider; default holds the identity of every other KB
  default:
    identity: Ada Lovelace <ada@example.com>   # D904; the git author of this machine's writes; without one, the identity git resolves in the session's repository (an HTTP entry: the global one)
  work-kb:
    access: read                # D1409; connects the KB read-only on every provider that receives it; edit with `cartographer client kb`
    artifacts: false            # D1408; stops the KB's skills, subagents, hooks and MCP descriptors (it stays connected)
    identity: Ada Lovelace <ada@corp.example>  # an entry decides: "" sends no author (the server names the token), an invalid one sends none and `sync` warns
    signing_keys: []            # Ed25519 public keys pinned per source KB (`connect --pin-key`); never learned from a sync
placeholders:                   # how {{repo:<key>}} and {{path:<name>}} resolve (D75, D1731)
  search_roots: ["~/Documents", "~/src", "~/code", "~/projects"]   # where repoindex.Scan looks for git clones for {{repo:<key>}}; the default, 4 levels deep (a constant, D906); a default root that does not exist is skipped silently; consulted only for a key with no `placeholders.paths` entry
  paths: {}                     # manual name -> path mapping for {{path:<name>}} (and an override for {{repo:<key>}}); edit with `cartographer paths set`
  ignored: []                   # placeholder ids known to be absent on this machine, e.g. repo:work-tools (D282); edit with `cartographer paths ignore`
approvals_migrated: false       # set by the first sync once it has approved what was already installed unchanged (D1166); never cleared, do not edit. Stays true after the approvals moved to the machine file (D1728)
update_check: true              # D254, D906; written only when false. false: no update lookup at all. The client never installs a release by itself
usage_scan: true                # D326; written only when false. false: sync reads no session transcript and reports no skill usage (docs/sync.md §Usage scan)
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
set, and a file written by an older version gains the comments at its next save. A comment the schema
wrote in the wording of an earlier release is rewritten to the current one at the next realignment (the
key's `Previous` descriptions, whatever default or values line went with them), and the comment of a
removed key goes with it, wherever an older realignment left it. A save that changes
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

**The groups (D1731).** The eight flat keys `server_url`, `server_name`, `auth`, `token`,
`allow_insecure_http`, `search_roots`, `paths` and `ignored_paths` moved into the `server:` group (`url`, `name`,
`auth`, `token`, `allow_insecure_http`) and the `placeholders:` group (`search_roots`, `paths`, `ignored`) through
the `Moved` entries of `Retired`: the first `sync` after the upgrade moves each value with the comment a person
wrote above it and keeps the original as `.cartographer.yaml.bak-<version>`; `Save` over a file it has not
reached moves them too (no backup, as for the keys `Save` drops). `Load` reads both layouts for at least one
minor, so a file on a read-only mount keeps working. A key set at both places with different values is an
error naming the two keys, from `Load`, `Save` and the realignment, which leaves the file as it is: keep
one. The report a client posts to Meridian and `cartographer config` mask a literal token at either place.

The per-provider and per-KB keys follow the same path. The realignment moves `clients`, `scopes`, `unbound_access` and
`workspaces` into `providers.<provider>` (`kbs`, `config_dirs`, `access`, `allow_symlinks`, `scope`, `unbound_access`, `workspaces`), with
`unbound_access.default` going to `defaults.unbound_access`; `kb_options`, `identities` and `signing_keys` go into
`knowledge_bases.<kb>` (`access`, `artifacts`, `identity`, `signing_keys`), with `identities.default` going to
`knowledge_bases.default.identity`. `clients.<p>.access: read-min`, one value for two things, is written as
`providers.<p>.access: read` plus `tools: minimal`; `Load` accepts only that pair (a lone `access: read` or
`tools: minimal` is an error naming the provider). A `*` segment in a `Moved` entry stands for every provider or KB the
file names, so one entry moves them all; a KB name with a dot or a `*` in it moves like any other (a KB is named after its folder), each key addressed literally. `Grouped`
renders a flat file in the current layout in memory, for the settings report and `cartographer config`. `mcp_approvals` and `plugins` are
not part of the groups.

**Key order.** The realignment that moves keys into their groups writes the whole file in the order of the
`Schema` table (the root keys, the keys of `server` and `placeholders`) and, inside a provider, `kbs`, `scope`,
`unbound_access`, `config_dirs`, `access`, `tools`, `allow_symlinks`, `workspaces`; inside a knowledge base, `access`, `artifacts`,
`identity`, `signing_keys`. A comment travels with its key, an unknown key stays right after the key that preceded it
in the file, and the order of the providers, the KBs and the list entries is the file's. A file that needs no move
keeps its order until `cartographer client realign --reorder`, which rewrites it in that order (the original is kept
as `.cartographer.yaml.bak-<version>`, a second run changes nothing); `cartographer client realign` alone is the
realignment of `sync` on demand. A KB name with a dot in it is an ordinary key for the reorder.

The registry holds the Compass keys `token_env` (rewritten as `token: ${NAME}`, comments kept),
`trust`, `write_findings_hook`, `search_depth`, `update.check` and `update.policy` (`update_check`, which an
`auto-patch` policy turns into `true`), `agents`, whose **value moves** to the Compass state file, and
`sync_timer_opt_out` and `mcp_approvals`, whose **values move** to the machine file (below)
before the YAML drops them. A realignment that cannot write those files stops and leaves the keys where they are. Until a file is realigned (a read-only mount), `Load` honours the old
`token_env`, `update.check`, `agents` and `sync_timer_opt_out`, and `mcp_approvals` while the machine file is absent.

**Machine-written records live in `.cartographer-state.json`**, next to `.cartographer-client.lock` and
`.cartographer-sync.lock.json` (D747). `known_kbs` (the KB names `/health` advertised at the last
`connect`/`sync`), `server_mount_mode` and `server_routed_path` are rewritten wholesale by every
sync, which is why they left the file you edit; since D906 it also holds `agents` (the connected clients,
maintained by `connect` and `disconnect`), for the same reason: they are written by commands, never by a person. Because of them it is
**not disposable**: a deleted state file means no connected client until the next `connect`. A sync that only refreshes them writes the state file
and leaves `.cartographer.yaml` byte-identical, mtime included. The state file is JSON, never
hand-edited. An installation from before keeps these keys in `.cartographer.yaml`; `Load` reads them
from there while the state file is absent, and the next save moves them (the legacy `kbs` key, read
since D169, goes the same way). An unreadable or corrupt state file is treated as absent, with one
line on stderr that names what to redo: the known KBs come back with the next `sync`, but the connected agents do not (`connect`); the next save keeps the unreadable bytes as `state.json.bak` before replacing the file. 

**The machine file, `.cartographer-machine.json` (D1728).** What Compass's own commands record and no sync can rebuild: the local approvals (`cartographer approve`; hooks, executable artifacts and MCP descriptors, under `<kind>/<name>` for the first two), the `approvals_migrated` marker and the opt-outs `sync_timer_opt_out` and `tray_opt_out`. JSON, mode 0600, never hand-edited, and **fail closed**, unlike the state file: if it exists and cannot be read or parsed, `Load` approves nothing, treats the pre-approval migration as done (so nothing installed is approved in its place) and warns that the file is unreadable; `Save` leaves it untouched, and a command that would record an approval or an opt-out stops with an error until the file is fixed or removed (removing it asks for every approval again). A machine with nothing to record has no such file. An installation from before keeps these keys in `.cartographer.yaml` (the approvals and the marker) and in the state file (the opt-outs): `Load` reads them while the machine file is absent, and the first save or `client realign` writes the machine file, then drops the old keys, after keeping `.cartographer.yaml.bak-pre-machine`. Once the machine file exists the old keys are ignored. `approvals_migrated: true` stays in the YAML as a tombstone and the two opt-outs are still copied into the state file for this minor, so an older client after a downgrade sees the marker set (it never re-approves what is installed), no approvals (it asks again) and its opt-outs. If the machine file later disappears (deleted, a dangling link) the tombstone makes `Load` approve nothing, migrate nothing and warn to give the approvals again with `cartographer approve`; the pre-migration YAML is the `.bak-pre-machine` copy. While the file is unreadable, `Save` and `client realign` keep the old YAML keys and stop rather than drop them.

`providers` is user-owned: `sync` never writes it, and `connect` writes a provider's binding only on its first connect, when it has none (the KB selection, `--kb`); it is otherwise maintained with `cartographer client` (below).

`.cartographer.yaml` is written atomically (a temporary file beside it, `fsync`, rename) and the operator's own file keeps what they gave it: a **symlink** (stow, yadm) is followed and its target replaced, so the link survives; an existing **mode** is kept (a literal token only ever takes group and other access away, never gives it). This differs from the no-follow rule of the Compass directories (D148) because it is the operator's own file, written on their own command, not a path inside somebody else's checkout. A key the file does not carry (`server.url`, `server.name`, `server.token`) loads as the schema's default, as `Default()` does; an explicit empty value stays empty.

### `cartographer client`

Declares which Knowledge Bases each connected provider may receive. Every
subcommand works without a network: it reads and writes `.cartographer.yaml`,
and only `client bind` asks Meridian for its KB list when it is reachable
(table below), so a KB name that is not currently advertised is a warning, not
a failure.

| Command | Effect |
|---|---|
| `client list` | one row per connected provider: resolved KBs and the origin of the answer (`explicit` / `default (all known)`) |
| `client show <provider>` | that provider's bound KBs, its origin, and the known KBs it is **not** bound to; for `claude` also a `config dirs` line: `$HOME` (default), each declared dir, and an undeclared `CLAUDE_CONFIG_DIR` marked `not declared` |
| `client bind <provider> <kb>[,<kb>...]` | adds to the binding (the output lists the resulting binding and what this call added); creating the first binding narrows the provider from "every known KB" to only those listed, and the output says so; it asks Meridian for its KB list once and refuses a KB Meridian does not mount (without rewriting the cached list, `sync` owns it, D721); with Meridian unreachable it falls back to the cached list and only warns; duplicates in the list are stored once |
| `client unbind <provider> <kb>[,<kb>...]` | removes; removing the last KB leaves the provider bound to **no** KBs |
| `client reset <provider>` | deletes the binding, returning the provider to the default |
| `client update [--check=true\|false]` | shows, or sets, `update_check` (D254, D906) |
| `client realign [--reorder]` | realigns `.cartographer.yaml` to the current settings table, the same pass `sync` runs (the original is kept as `.cartographer.yaml.bak-<version>`); `--reorder` also writes the keys in the table's order, leaving a mapping as it is when one of its keys aliases (or merges) an anchor another of its keys defines |
| `client symlinks <provider> [--add <path>] [--remove <path>]` | lists, adds or removes the provider's `allow_symlinks` entries (§Symlinked config files and directories are refused unless listed); entries are compared expanded and cleaned, a `--remove` that matches nothing exits 1, and an entry that is `~`, `$HOME`, `/`, the home directory or holds a control character is refused |
| `client kb <kb> [--artifacts=true\|false] [--access=read\|write]` | shows, or sets, the KB's per-KB options (the `knowledge_bases` map, D1408, D1409), for every provider; `--access=read` connects the KB read-only (below), `write` removes the key; `false` stops `sync` from provisioning that KB's skills, subagents, hooks and MCP descriptors and removes the ones installed, while the KB stays connected; `true` removes the key; a KB Meridian does not advertise is a warning, not a failure; prints `run cartographer sync to apply` |

`knowledge_bases.<kb>.artifacts: false` (D1408) is a narrowing, never a widening: it wins over `providers.<provider>.scope`, `providers.<provider>.kbs`, explicit workspace bindings and derived bindings, and changes neither which KBs a provider or folder reaches nor the KB's MCP tools. It does nothing on a server that does not name its KBs. `status` marks such a KB `artifacts: off (knowledge_bases)` under the provider's `kbs` line.

**`knowledge_bases.<kb>.access: read` (D1409).** The one KB is connected read-only on every provider that receives it, while the provider's other KBs stay writable; absent or `write` is the default and changes nothing in any URL. `sync` writes `readonly=<kb>,…` into the routed entry after `kbs=` and `access=` (only the KBs the entry reaches, escaped like `kbs`), and the workspace bridge sends the same parameter, so it applies in a bound folder too. With `providers.<provider>.access: read` and `tools: minimal` the provider is already read-only: no `readonly=` is written. With `providers.<provider>.scope` and `unbound_access` the stricter wins: an unbound read-only folder already covers everything, a bound folder is read-write except for the KBs marked `read`. It is a scope filter, not a security boundary (D737): a client that edits its own URL drops it; hard isolation is a server token role (`kb:<name>:r`). It needs a routed server: against a server without the routed endpoint `connect` and `sync` refuse, naming the KB and the fix, instead of writing an entry that would allow writes. The bridge fails closed on a server that predates the parameter (it sends no `Cartographer-Readonly` header), but a plain HTTP entry cannot check a header, so an entry written for a server older than this version ignores the setting silently: upgrade Meridian first.

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
wrong and are pinned by tests — the entry **shape** comes from what Meridian
mounts (a bare `/mcp` auto-routes only when Meridian mounts exactly one KB, so
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

## Bare `cartographer`

With no arguments in a terminal, `cartographer` prints a short status of this machine, a blank line,
then the usage (D1857). It is a help screen, not a check: it always exits 0
(`cartographer status` is the command with meaningful exit codes), it never writes to disk, and
the one remote read is the one `status` makes, bounded by 5 s. What the status says:

- no client config: ``next: run `cartographer setup` ...`` (D253), no network;
- a config that cannot be read: `client config cannot be read (fix or remove <path>): <err>`, no network;
- a config with no agent connected: ``no agent connected (run `cartographer connect`)`` and the plugin
  section;
- otherwise the Meridian lines exactly as `status` prints them (header or error, version skew, updates
  available, configuration drift against Meridian), `next: <hint>` when the machine needs
  `setup`, one `  [<provider>] <state>` line per connected provider (`in-sync`, ``drift - run `cartographer sync` ``,
  `unknown`, ...) and `details: cartographer status | cartographer doctor`.

Per-artifact lists, kinds, bindings and the plugin table are `cartographer status`. Outside a terminal,
`cartographer` with no arguments prints only the usage (like `cartographer help`).

## Non-destructive merge

If a config file already exists, `connect` reads the existing content, adds or
updates only the Cartographer entry (`server_name`, default `cartographer`) and rewrites the file. Other configuration present
in the file (other MCP servers, other keys) is left untouched. The merge is at the level of the
server map: the Cartographer entry is **replaced whole**, so a header or an `args` element the new
spec no longer has does not survive. `server_name` is letters, digits, `_` and `-` only (the
emitters refuse anything else: it is an entry key, a bare TOML key, a tool prefix and a URL part). `disconnect` performs the
inverse operation (`configurator.Remove`): removes only the Cartographer entry (`server_name`) from the `mcpServers`/`mcp` map and
rewrites the file, leaving everything else intact (other MCP servers, other top-level keys).

### Symlinked config files and directories are refused unless listed

Every write, mkdir and remove under a client directory goes through `internal/safewrite`, the same
guard that protects the provisioned artifacts (`sync.md` §Security): the provider's MCP config file
and the Codex `config.toml` blocks `connect`, `sync` and `disconnect` edit (`internal/blocktext`),
the instructions block, the hook registrations (`settings.json`, `hooks.json`, the Kiro, Copilot,
Crush, Pi and OpenCode files), the files `disconnect` and a sync prune, and the workspace's
`.git/info/exclude`. `os.WriteFile` on a symlink opens the link's *target*, and `os.Remove` or
`os.MkdirAll` under a linked directory act in the target too, so a config file that is a link, or a
directory above it under the Compass base dir that is one (a dotfile manager, a shared checkout), would send the
operation somewhere else. Both are refused, with an error that names the path and says to replace the
link with a regular file or directory or to list it under `providers.<provider>.allow_symlinks`; so is any other non-regular file or non-directory (device,
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
  artifact. After the warnings `connect` names the outcome per provider ([D1722](decisions/D1722-connect-status-exit-three.md)):
  `connected: <p>` when nothing was refused; `partially connected: <p> (skipped: <parts>: destination is a symlink)` when some
  parts were written and some refused; `not connected: <p> (<parts> skipped: destination is a symlink)` when the MCP entry was
  refused, whatever else was written, because without it the agent cannot reach Cartographer. The parts are what the run
  refused: `MCP entry`, `bootstrap hook`, `write-findings hook`, and the kinds of artifact (`skills`, `agents`, `hooks`,
  `instructions`). A refused *removal* (`<kind>/<name> not removed`) stays a warning and does not count as a skipped part: the provider is still wired. `wrote MCP entry <name>` is printed (by `connect` and `sync`) only for an entry that was written. **`connect`
  exits 3** when at least one provider ended partially or not connected; the others are still wired, so 3 is the signal, not an
  abort (0 and 1 keep their meaning, 2 stays configuration/operational); `reconnect` exits 3 the same way, and so does a real `sync` that was refused a write (#2099). The bootstrap and write-findings hooks are no exception: a provider whose
  destination for them is behind a link gets a `<provider>: bootstrap hook not installed: …` warning and the
  others are still set up. A refused write of a new artifact is not recorded as installed, a refused update keeps the previous
  lockfile entry (its files on disk are still ours), and one whose removal was refused stays in the
  lockfile, so the next sync retries each. A hook's script is only removed once its
  registration is. When a stale per-KB entry could not be removed, that KB (only) stays in
  `known_kbs` until the entry is gone. A workspace's `info/exclude` behind a link, or a `.git` file that leads to no git directory, is a warning, not
  an error.
- `disconnect` prints `kept connected: …` for the provider, leaves its lockfile section,
  `.cartographer.yaml` entry and managed files (whatever could be pruned is) in place so the next
  `disconnect` retries, removes the others, and **exits 1**. `reconnect`
  does not rebuild a provider its disconnect half kept, rebuilds the others, and also exits 1.

Nothing is written through the link until the operator replaces it or lists it (below). The legacy `.codex/config.json`
that `disconnect` cleans up is checked before `config.toml`, so a refusal there leaves both untouched.

#### Following a listed link: `providers.<provider>.allow_symlinks`

The refusal stays the default. A user who keeps agent config in a dotfiles repository (GNU Stow,
chezmoi) lists the links to follow, per provider, in `.cartographer.yaml`:

```yaml
providers:
  opencode:
    allow_symlinks: [$HOME/.config/opencode]
```

- An entry is the **link itself** (a file link such as `$HOME/.claude.json`, or a directory link),
  absolute or starting with `~/` or `$HOME/`; anything else, an entry holding a control character, and `~`, `$HOME`, `/` or the home directory itself make the file fail to load. Matching is lexical on the
  logical path Compass addresses, so spell the link, not its target.
- A listed **directory link covers everything below it**; a link nested below a listed one needs its own entry.
- The set is the union of every provider's entries: safewrite does not know the provider, and a path listed
  under one is a path the user listed.
- When a listed link points at another link, every hop of the chain must be listed too.
- The link is kept and the write lands on its target (a leaf link is resolved and the target replaced atomically, the
  target's mode kept). A followed leaf link is never removed and its target never deleted: a cleanup that would delete
  the file writes the emptied content through the link instead.
- A target that is not writable (a read-only store or mount) is a per-artifact refusal naming the link and the target.
- `TrustCodexProject` never follows a link, listed or not.
- **The key is read only from the user's own `.cartographer.yaml` in the home directory**, never from a file in a
  repository or the working directory: a cloned repository cannot choose which links are followed. An unreadable
  config registers nothing, so the refusal stays.
- `cartographer client symlinks <provider> [--add <path>] [--remove <path>]` edits the list (with no flag it lists
  each entry and its target; `--add` warns when the path is missing or not a link, since a link may not exist yet)
  and prints `run cartographer sync to apply`. A long-running process reads the key at its start.
- `--dry-run` prints `would write through symlink X -> Y` once per followed link and writes nothing; it does not
  probe whether the target is writable.
- Lockfile paths stay logical (relative to the base dir), never a resolved target.

## Installation

One channel per platform, and an upgrade uses the one the binary came from:

```bash
brew install beppetemp/tap/cartographer     # macOS (cask from the tap)
irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex   # Windows (PowerShell, D252)
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
```

`install.sh` downloads the latest binary from the GitHub Release for the current platform (darwin/linux ×
amd64/arm64, plus linux/arm for ARMv7), verifies the checksum against the release's `sha256sums.txt` (a missing manifest is an error, D1022), and installs it into
`/usr/local/bin` (or `~/.local/bin` if not writable). Also supports `update` and `uninstall` as the
first argument. Run from a Windows shell (Git Bash, MSYS2, Cygwin) it refuses and prints the `install.ps1`
command instead. `install.ps1` is the Windows counterpart (install/`update`/`uninstall`, per-user,
checksum-verified). See `docs/deployment.md` §Client installation.
