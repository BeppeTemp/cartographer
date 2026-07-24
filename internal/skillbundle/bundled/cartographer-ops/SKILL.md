---
name: cartographer-ops
description: Configure, operate, troubleshoot, or upgrade a Cartographer server or client; connect agents and manage Knowledge Bases.
version: "1.0"
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
| `cartographer serve` | Starting the server directly, for development or a custom deployment. |
| `cartographer service install\|status\|restart` | Installing, checking, or restarting the native local service. |
| `cartographer kb create <name>` | Creating the first local KB in the service data directory. |
| `cartographer kb clone <remote> [name]` | Mounting an existing OKF KB remote in the service data directory. |
| `cartographer connect [provider\|all] [--agents a,b]` | Connecting one or more detected agent clients to a server. |
| `cartographer status` | Checking client configuration and provisioning drift. |
| `cartographer sync` | Realigning provisioned skills, agents, hooks, and instructions. |
| `cartographer disconnect [provider\|all] [--agents a,b]` | Removing Cartographer-managed client configuration. |
| `cartographer version` | Checking the installed binary version. |

`--agents claude,codex` selects a comma-separated subset for `connect` or `disconnect`; do not
combine it with the positional provider. `connect` probes the server before writing: a reachable
server with no KBs needs `kb create` followed by `service restart`.

## Configuration

Server settings resolve as **flag > environment > YAML > default**. Keep secrets in environment
variables or the platform secret store, not in a committed YAML file.

| Variable | Purpose |
|---|---|
| `CARTOGRAPHER_KB` | One or more explicit KB paths. |
| `CARTOGRAPHER_DATA` | Data directory whose direct subfolders are discovered as KBs. |
| `CARTOGRAPHER_HTTP` | HTTP listen address, such as `:8080`. |
| `CARTOGRAPHER_AUTH` | Explicit auth mode: on, off, or automatic when unset. |
| `CARTOGRAPHER_TOKENS` | Bearer tokens, including optional per-KB scopes. |
| `CARTOGRAPHER_GIT_SYNC` | Set `false` only when remote git synchronization around writes is intentionally disabled. |
| `CARTOGRAPHER_OLLAMA` | Ollama base URL that enables semantic search. |

## Diagnosis playbook

1. Check `GET /health`. Its `status`, `version`, `ready`, and `kbs` fields distinguish a live
   process from a usable server.
2. If `ready` is false or no KBs are mounted, run `cartographer kb create <name>` and then
   `cartographer service restart` for a local service.
3. If client artifacts are stale or missing, run `cartographer sync`, then restart the affected
   agent session so it reloads MCP configuration and skills.
4. If concepts are degraded after a git conflict, use the `kb-conflict-resolve` skill; do not
   repair their files outside Cartographer.
5. If the binary and running service differ, run `cartographer service restart` after upgrading.

## Upgrade

- macOS: `brew upgrade --cask beppetemp/tap/cartographer`, then `cartographer service restart`.
- Kubernetes: update the Cartographer image tag in the deployment manifest and wait for rollout.

## Never do

- Do not edit KB concept files directly: use Cartographer MCP tools so validation, git commits,
  and synchronization invariants apply.
- Do not hand-edit provisioned MCP entries or `.cartographer.yaml`; use `connect`, `sync`, and
  `disconnect` so Cartographer can track and safely prune its own files.

## Further reading

- https://github.com/BeppeTemp/cartographer/blob/main/docs/deployment.md
- https://github.com/BeppeTemp/cartographer/blob/main/docs/configurator.md
