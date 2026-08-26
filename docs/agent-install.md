# Agent-driven installation

Use this runbook when the user supplies the Cartographer repository link and the git remote for
their first Knowledge Base (KB). A KB is a git repository and its remote is what makes it durable
and syncable, so the remote is required: if the user did not supply one, **ask for it before step
3** — an empty repository they own (GitHub, Gitea, or any git host). Execute every command in
order; report the expected result before continuing.

## 1. Install Cartographer

Detect the platform:

```bash
uname -s
```

Expected output: `Darwin` on macOS, or the name of another supported Unix-like platform. On macOS,
first check for Homebrew:

```bash
command -v brew
```

Expected output: the path to `brew`. If it is present, install Cartographer:

```bash
brew install beppetemp/tap/cartographer
```

Expected output: Homebrew reports that `cartographer` was installed. If `brew` is absent, install
the current release instead:

```bash
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
```

Expected output: the installer reports the destination of the `cartographer` binary.

Confirm the binary is available:

```bash
cartographer version
```

Expected output: a Cartographer version.

## 2. Install the local service

```bash
cartographer service install
```

Expected output: the native user service is installed and started. It listens on
`http://127.0.0.1:39273` and its data directory is ready for KBs.

## 3. Mount the first KB

When the remote already contains a KB, mount it through Cartographer:

```bash
cartographer kb clone <git-remote-url> --restart
```

Expected output: `KB "<name>" mounted at <data-dir>/<name>`, followed by service restart and
health guidance. Do not clone the repository into the service data directory yourself.

When the remote is an empty repository, create the KB in it:

```bash
cartographer kb create <name> --remote <git-remote-url> --restart
```

Expected output: `KB "<name>" created at <data-dir>/<name>`, then `origin: <git-remote-url>`,
followed by `service healthy`.

Ask for a remote before running either command. Only if the user explicitly accepts a throwaway,
local-only KB — not backed up and never synchronized — fall back to `cartographer kb create <name>
--no-remote --restart`, and state that limitation back to them.

## 4. Connect the executing agent

First identify the installed provider name:

```bash
cartographer agents
```

Expected output: a table listing `claude`, `opencode`, `codex`, and `kiro` with installation state.
Connect the executing provider with `--agents`; for example, for Codex:

```bash
cartographer connect --agents codex
```

Expected output: the generated MCP configuration paths and a reminder to restart the agent session.
With two or more mounted KBs, Cartographer automatically creates one MCP entry per KB.

## 5. Verify the installation

```bash
cartographer version
curl -fsS http://127.0.0.1:39273/health
cartographer status
```

Expected output: a version, then health JSON containing `"ready":true`, then in-sync status with
exit code 0. Restart the connected agent session after this check so it loads the MCP tools and
provisioned skills.

`connect` provisioned the bundled skills, including `cartographer-ops`. Use that skill for ongoing
operations, diagnosis, upgrades, and synchronization after installation.

## Failures

| Observed symptom | Next action |
|---|---|
| `command -v brew` has no output | Run the `install.sh` command in step 1. |
| The service reports that port 39273 is busy | Stop or reconfigure the process using the port, then rerun `cartographer service install`. |
| `kb clone` reports a git authentication failure | Configure ambient credentials (an SSH agent for SSH remotes or a git credential helper for HTTPS), then rerun the same `kb clone` command. |
| `kb clone` says `not an OKF KB` | Use the `kb-import` skill to import the remote into an OKF KB, push it, then rerun `kb clone`. |
| `kb create` says a KB needs a git remote | Ask the user for an empty repository URL and rerun with `--remote <url>`; use `--no-remote` only if they explicitly accept a local-only KB. |
| `kb create --remote` fails to push (non-fast-forward, or the remote is not empty) | The repository already has content: mount it with `cartographer kb clone <git-remote-url> --restart` instead. |
| `kb create --remote` reports a git authentication failure | Configure ambient credentials (an SSH agent for SSH remotes or a git credential helper for HTTPS), then rerun the same command — the failed scaffold was already removed. |
