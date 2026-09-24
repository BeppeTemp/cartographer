---
name: cartographer-ops
description: Configure, operate, troubleshoot, or upgrade a Cartographer server or client; connect agents and manage Knowledge Bases.
version: "1.6"
---
# Cartographer Operations

## Mental model

Cartographer is one server process over a data directory whose direct subfolders are Knowledge
Bases (KBs). Connect clients to it with `cartographer connect`; agents use the MCP server rather
than editing KB files. Every KB write is committed to git, and a configured remote is synchronized
around writes.

## CLI surface

| Command | Use it when |
|---|---|
| `cartographer setup` | First-time setup of this machine: service, first KB and agents, verified (see below). |
| `cartographer serve` | Starting the server directly, for development or a custom deployment. |
| `cartographer service install\|status\|restart` | Installing, checking, or restarting the native local service. |
| `cartographer kb create <name> --remote <url>` | Creating the first local KB in the service data directory, pushed to an empty repository that becomes its `origin`. `--no-remote` opts out into a local-only KB that is neither durable nor synced; the choice is mandatory. |
| `cartographer kb clone <remote> [name]` | Mounting an existing OKF KB remote in the service data directory. |
| `cartographer connect [provider\|all] [--agents a,b]` | Connecting one or more detected agent clients to a server. |
| `cartographer status` | Checking client configuration and provisioning drift. |
| `cartographer sync` | Realigning provisioned skills, agents, hooks, and instructions. |
| `cartographer disconnect [provider\|all] [--agents a,b]` | Removing Cartographer-managed client configuration. |
| `cartographer version` | Checking the installed binary version. |
| `cartographer update check` | Checking whether a newer release exists, and the upgrade command for this install. |

`--agents claude,codex` selects a comma-separated subset for `connect` or `disconnect`; do not
combine it with the positional provider. `connect` probes the server before writing: a reachable
server with no KBs needs `kb create --remote <url>` followed by `service restart` — or simply
`cartographer setup --remote <url>`.

## First-time setup

`cartographer setup` sets a machine up in one step: the native service, the first KB (created in
an empty repository, or mounted from one that already holds a KB — it asks the remote with
`git ls-remote`) and the agent clients, then a health check. It checks git and the remote's
credentials before changing anything, and a rerun skips what is done.

When you run it for a user, interview them first, in **one** message:

1. the git remote of the first KB — an empty repository they own, or one holding their KB; a
   local-only KB only if they explicitly accept that it is never backed up or synced;
2. which agent clients to connect — default: the one you are running in;
3. whether the KB should be visible everywhere (default) or only inside one repository.

Then preview and run, never answering its questions on the user's behalf:

```bash
cartographer setup --remote <url> --agents <a,b> [--workspace <repo>] --dry-run --no-input
cartographer setup --remote <url> --agents <a,b> [--workspace <repo>] --yes --no-input
```

If the plan's KB line says *mount* for a repository the user called empty (or *create* for one
they said holds their KB), stop and tell them. On a machine that already mounts two or more KBs,
ask which ones the agents receive and pass `--kb`. End by telling the user to restart their agent
sessions.

## Configuration

Server settings resolve as **flag > environment > YAML > default**. Keep secrets in environment
variables or the platform secret store, not in a committed YAML file.

| Variable | Purpose |
|---|---|
| `CARTOGRAPHER_KB` | One or more explicit KB paths. |
| `CARTOGRAPHER_DATA` | Data directory whose direct subfolders are discovered as KBs. |
| `CARTOGRAPHER_HTTP` | HTTP listen address, such as `:39273`. |
| `CARTOGRAPHER_AUTH` | Explicit auth mode: on, off, or automatic when unset. |
| `CARTOGRAPHER_TOKENS` | Bearer tokens, including optional per-KB scopes. |
| `CARTOGRAPHER_GIT_SYNC` | Set `false` only when remote git synchronization around writes is intentionally disabled. |

## Diagnosis playbook

1. Check `GET /health`. Its `status`, `version`, `ready`, and `kbs` fields distinguish a live
   process from a usable server.
2. If `ready` is false or no KBs are mounted, ask for the git remote the KB belongs to, then run
   `cartographer kb create <name> --remote <url>` (or `cartographer kb clone <url>` if that
   repository already holds a KB) and `cartographer service restart` for a local service.
3. If client artifacts are stale or missing, run `cartographer sync`, then restart the affected
   agent session so it reloads MCP configuration and skills.
4. If concepts are degraded after a git conflict, use the `kb-conflict-resolve` skill; do not
   repair their files outside Cartographer.
5. If the binary and the running service differ, run `cartographer upgrade-repair` (D121): it
   gracefully replaces a running server, proves the new version is serving, and reconciles the
   configured providers. A bare `service restart` does none of that and can leave a client
   configured against the previous version.
6. If writes fail and `sync_status` reports `degraded` with `branch` different from
   `remote_default_branch`, the KB's clone is on a branch other than the remote's default one
   and Cartographer refuses to write rather than fork the KB on the remote (D264). Reads keep
   working. Show the user `last_error`, which names both branches and the clone path; recovery
   is theirs to choose: merge the stray branch into the default branch on the remote (a pull or
   merge request), or check out the default branch in the clone. Then
   `cartographer service restart`. Never merge, rebase, push or delete a branch yourself.

## Upgrade

Use the channel the binary came from; installing over it from a different one is how two
Cartographers end up on `PATH`.

- macOS: `brew upgrade --cask beppetemp/tap/cartographer`. The next `cartographer sync` (every
  agent session start runs one) replaces the running service with the new binary and re-syncs,
  so **no follow-up command is needed**; run `cartographer upgrade-repair` to do it immediately.
- Windows: `install.ps1 update`, from PowerShell:
  `& ([scriptblock]::Create((irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1))) update`.
  It verifies the checksum, swaps the binary even while the native service is running from it,
  and runs `upgrade-repair` like the POSIX installer. Do not download or extract the zip by hand.
- POSIX installer (Linux, macOS without Homebrew): `install.sh update`, which runs
  `upgrade-repair` the same way.
- Kubernetes: update the Cartographer image tag in the deployment manifest and push it, then
  end on a **verified image**, not on a rollout:
  1. Wait for the manifest to be applied to the cluster — immediately if you apply it yourself,
     otherwise by the GitOps controller that owns the manifests repo, if there is one, which may
     lag by its reconcile interval.
  2. Verify the Deployment carries the new tag —
     `kubectl get deploy/cartographer -o jsonpath='{.spec.template.spec.containers[0].image}'` —
     **before** waiting for rollout. Until it does, `kubectl rollout status` returns success in
     under a second, truthfully, about the old ReplicaSet that has nothing left to roll out.
  3. Then wait for rollout, and confirm with the version the server reports: `cartographer status`
     prints it as `server vX`.

**Removing it has an order**, whatever the channel: `cartographer service sync-timer uninstall`,
`cartographer service uninstall` and `cartographer disconnect` first, with the binary still in
place, then the channel's own removal. `install.sh uninstall` and `install.ps1 uninstall` refuse
while the service or the timer is installed; `brew uninstall` does not check, so a launchd agent
would be left pointing at a missing executable.

Only already-open agent sessions need restarting after an upgrade, so they reload the MCP
configuration and the provisioned skills. Tell the user to do that — the agent cannot restart its
own session.

## Update notices

A session can start with a line from `cartographer update notice` saying a newer release exists,
or `cartographer status` / `kb_status` can report one (`update available`, `latest_version`).

- Tell the user **once** per session, with the version and the command the notice names.
- Offer to run that command; run it **only if** the user explicitly says yes. Never upgrade on
  your own initiative, and never switch to a different channel than the one named.
- After it succeeds: `cartographer upgrade-repair` (`install.sh` and `install.ps1` already run it),
  `cartographer reconnect` only when the release notes ask for it, then tell the user to restart
  their agent sessions.
- A remote or cluster server is not yours to upgrade from a client: propose the procedure
  (Kubernetes, above) to whoever operates it.
- `cartographer client update --check=false` turns the check off; `--policy auto-patch` lets
  patch releases install themselves (Homebrew, `install.sh`, `install.ps1` only). Change either
  only when the user asks.

## Never do

- Do not edit KB concept files directly: use Cartographer MCP tools so validation, git commits,
  and synchronization invariants apply.
- Do not run git in a KB's clone (`init`, `checkout`, `push`, …): the server owns it, and a
  branch created there is how a KB forks on its remote. Report `sync_status` instead.
- Do not hand-edit provisioned MCP entries or `.cartographer.yaml`; use `connect`, `sync`, and
  `disconnect` so Cartographer can track and safely prune its own files.

## Further reading

- https://github.com/BeppeTemp/cartographer/blob/main/docs/deployment.md
- https://github.com/BeppeTemp/cartographer/blob/main/docs/configurator.md
