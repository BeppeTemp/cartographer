---
name: cartographer-ops
description: Configure, operate, troubleshoot, or upgrade a Cartographer server or client; connect agents and manage Knowledge Bases.
version: "2.0"
---
# Cartographer Operations

## Mental model

One server process (Meridian) serves a data directory whose direct subfolders are Knowledge Bases
(KBs). Each KB is a git repository. Every write through an MCP tool is one commit, and a KB with a
remote is synchronized around writes; a KB without one is never backed up or synced. The client
commands (`connect`, `status`, `sync`) configure agent clients to use the server and keep their
skills, hooks and instructions aligned. Agents use the MCP tools, never the KB files.

For any command, flag, key or error: `cartographer docs graph_context query="<content words>"`
(skill `cartographer-docs`); `cartographer setup` is the first command (below).

`connect` probes the server before writing. A reachable server with no KB needs
`cartographer kb create <name> --remote <url> --restart` (local service; or `cartographer setup --remote <url>`).

## First-time setup

`cartographer setup` does it in one step: the native service, the first KB (created in an empty
repository, or mounted from one that already holds a KB — it asks the remote with
`git ls-remote`), the agent clients, then a health check. It checks git and the remote's
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

A server setting resolves as **flag > environment variable > YAML file > default**. Keep secrets in
environment variables or the platform secret store, never in a committed YAML file.
`cartographer config` shows every setting with its value and where it comes from; the variables are
in the docs (`cartographer docs search query="CARTOGRAPHER_DATA"`).

## Diagnosis playbook

Each step ends on a query for the details: `cartographer docs graph_context query="..."`.

1. `cartographer doctor`, then `cartographer status`: they name what is missing or stale.
2. `GET /health` (`status`, `version`, `ready`) tells a live process from a usable server.
3. `ready` false or no KB mounted: ask for the git remote, then `cartographer kb create` or
   `cartographer kb clone` (query: kb create remote).
4. Stale or missing client artifacts: `cartographer sync`, then restart the agent session.
5. Concepts degraded after a git conflict: the `cartographer-kb-conflict-resolve` skill; never
   repair their files outside Cartographer.
6. Binary and running service differ: `cartographer upgrade-repair` (a bare `service restart` does
   not reconcile clients). Exit 1: the service is fine, retry `cartographer sync`; exit 2: read
   `cartographer service status`.
7. Writes fail or a KB does not push: read `sync_status` (not in `tools/list`: call it by name,
   with `kb`); its `state` and `last_error` name the case (query: sync_status wrong branch
   protected branch author). Report what you find; the recovery is the user's. Never merge,
   rebase, push or delete a branch yourself. Do not retry a protected-branch refusal in a loop:
   the remote's answer will not change. For a refusal because of the commits' author, run
   `cartographer kb reauthor <kb>` without `--apply` first, show the commits it would change, and
   apply only on the user's consent.
8. A sync warns that a `{{repo:...}}` or `{{path:...}}` placeholder is not resolved:
   `cartographer paths list` (query: placeholder paths set).
9. Asked to see a KB, give the Atlas link: `ui_url` in `kb_status` (present only when the agent
   runs on the server's own machine), or the `ui:` line of `cartographer service status` on the
   server host. No `ui_url` means stdio, `CARTOGRAPHER_WEB_ENABLED=false`, a stopped service,
   or a server on another machine or behind a proxy: give the address the user reaches the server
   by, with `/ui/?kb=<name>`.

## Upgrade

Use the channel the binary came from; installing over it from another one leaves two
Cartographers on `PATH`.

- macOS: `brew upgrade --cask beppetemp/tap/cartographer`. The next `cartographer sync` (every
  agent session start runs one) replaces the running service with the new binary, so **no
  follow-up command is needed**; run `cartographer upgrade-repair` to do it immediately.
- Windows: `install.ps1 update`, from PowerShell:
  `& ([scriptblock]::Create((irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1))) update`.
  It verifies the checksum, swaps the binary even while the service runs from it, and runs
  `upgrade-repair`. Do not download or extract the zip by hand.
- Windows + WSL: run the upgrade in both, `install.ps1 update` on Windows and `install.sh update`
  in the distro; they must match, and `cartographer.exe service` warns on a skew.
- Linux, and macOS without Homebrew: `install.sh update`, which also runs `upgrade-repair`.
- Kubernetes: change the image tag in the deployment manifest and push it. With the Helm chart
  the tag is the chart's version: `helm upgrade <release> oci://ghcr.io/beppetemp/charts/cartographer --version <version>`,
  or change the version wherever the release is pinned. Then end on a **verified image**, not on
  a rollout:
  1. Wait for the manifest to be applied: at once if you apply it yourself, otherwise when the
     GitOps controller that owns the manifests repo reconciles it.
  2. Read the image back **before** waiting for rollout:
     `kubectl get deploy/cartographer -o jsonpath='{.spec.template.spec.containers[0].image}'`.
     For a chart release, select the Deployment:
     `kubectl get deploy -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=meridian -o jsonpath='{.items[0].spec.template.spec.containers[0].image}'`.
     Until it shows the new tag, `kubectl rollout status` returns success in under a second,
     truthfully, about the old ReplicaSet.
  3. Then wait for rollout, and confirm with the version the server reports: `cartographer status`
     prints it as `server vX`.

**Removing it has an order**, whatever the channel: with the binary still in place, run
`cartographer service sync-timer uninstall`, `cartographer service tray uninstall`,
`cartographer service uninstall` and `cartographer disconnect`, then the channel's own removal.
`install.sh uninstall` and `install.ps1 uninstall` refuse while the service or a timer is
installed; `brew uninstall` does not check, so a launchd agent would be left pointing at a missing
executable.

After an upgrade only the already-open agent sessions need restarting, to reload the MCP
configuration and skills. Tell the user to do it: the agent cannot restart its own session.

## Update notices

A session can start with a line from `cartographer update notice` saying a newer release exists,
or `cartographer status` / `kb_status` can report one (`update available`, `latest_version`).

- Tell the user **once** per session, with the version and the command the notice names.
- Offer to run that command; run it **only if** the user explicitly says yes. Never upgrade on
  your own initiative, and never switch to a different channel than the one named.
- After it succeeds: `cartographer upgrade-repair` (`install.sh` and `install.ps1` already run it),
  `cartographer reconnect` only when the release notes ask for it, then tell the user to restart
  their agent sessions.
- A remote or cluster server is not yours to upgrade from a client: propose the Kubernetes
  procedure above to whoever operates it.
- `cartographer client update --check=false` turns the check off (`update_check: false`). The client never installs a
  release by itself. Change it only when the user asks.

## Never do

- Do not edit KB concept files directly: use the MCP tools, so validation, git commits and
  synchronization apply.
- Do not run git in a KB's clone (`init`, `checkout`, `push`, `merge`, `rebase`, …): the server
  owns it, and a branch created there is how a KB forks on its remote. Report `sync_status` (not in
  `tools/list`) instead. The one sanctioned rewrite of unpushed commits is `cartographer kb reauthor <kb>`.
- Do not hand-edit provisioned MCP entries or `.cartographer.yaml`: use `connect`, `sync` and
  `disconnect`, so Cartographer can track and prune its own files.

## Further reading

Every other question about Cartographer: the `cartographer-docs` skill
(`cartographer docs graph_context query="<content words>"`).
