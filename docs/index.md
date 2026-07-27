# Cartographer — documentation index

Entry point for users, agents and contributors. Read this file first, then
only the pages relevant to your task.

## Using Cartographer

| File | Contents |
|---|---|
| [getting-started.md](getting-started.md) | Zero-to-wiki tutorial: install, first KB, connect an agent, first session |
| [agent-install.md](agent-install.md) | Command-by-command runbook for an agent to install Cartographer, mount a first KB, connect, and verify |
| [deployment.md](deployment.md) | Native/k8s topologies, server configuration, health, backup/DR, upgrades and CI/CD |
| [configurator.md](configurator.md) | Multi-provider client (`cartographer agents/connect/disconnect/status/sync` + TUI): flags, generated files per provider, `.cartographer.yaml`, lockfile v2, installation |
| [control-plane.md](control-plane.md) | Go server, complete MCP tool API (source of truth for the list), search index and validation |
| [data-plane.md](data-plane.md) | KB model: Atlas/Map/Journal hierarchy, filesystem layout, expanded concepts, OKF concepts, naming, extended type schema |
| [skills-services-secrets.md](skills-services-secrets.md) | Skill packaging (`SKILL.md`), `type: Service` descriptors, SOPS secrets |
| [sync.md](sync.md) | Client ↔ server provisioning sync: manifest+revision, lockfile, drift detection, layered triggers, prune, path-portability placeholders |
| [use-cases.md](use-cases.md) | Executable patterns for runbooks, imports, provider sharing and multi-KB separation |

## Internals & contributing

| File | Contents |
|---|---|
| [overview.md](overview.md) | Current architecture, runtime shapes, principles and component diagram |
| [concurrency.md](concurrency.md) | Single-writer boundary, git sync, `if_match` and conflict registry |
| [transport-auth.md](transport-auth.md) | stdio / HTTP transport, static bearer tokens, per-KB scopes and statelessness |
| [loop.md](loop.md) | Current read, write, validate and lint workflow |
| [interoperability.md](interoperability.md) | OKF boundaries and ownership of provider-specific compatibility documentation |
| [decisions.md](decisions.md) | Short router to topic-based decision records under `docs/decisions/`; decisions explain why, while topic docs define current behavior |
| [conventions.md](conventions.md) | Go conventions (language, style, errors, data-plane safety, tests, dependencies) |
| [testing.md](testing.md) | Deterministic unit, smoke and end-to-end strategy plus release checks |
| [references.md](references.md) | Bibliography (Karpathy, OKF, MCP spec, SOPS, agentskills.io) |

## Project state

Mutable state does not live in narrative documentation:

- [open plan issues](https://github.com/BeppeTemp/cartographer/issues?q=is%3Aissue+is%3Aopen+label%3Aplan) — approved implementation handoffs;
- [open enhancements](https://github.com/BeppeTemp/cartographer/issues?q=is%3Aissue+is%3Aopen+label%3Aenhancement) and [bugs](https://github.com/BeppeTemp/cartographer/issues?q=is%3Aissue+is%3Aopen+label%3Abug) — backlog;
- [pull requests](https://github.com/BeppeTemp/cartographer/pulls), [releases](https://github.com/BeppeTemp/cartographer/releases) and [`CHANGELOG.md`](https://github.com/BeppeTemp/cartographer/blob/main/CHANGELOG.md) — delivery and completed user-visible work.

## Quick reading paths

| Goal | Read |
|---|---|
| New user | `getting-started.md` → `deployment.md` (for an always-on or shared server) |
| New to the codebase | `overview.md` → `data-plane.md` → `control-plane.md` |
| Add an MCP tool | `control-plane.md` §tools → `conventions.md` → `AGENTS.md` (recipe) |
| Debug concurrency / git conflicts | `concurrency.md` |
| Add a skill or external service | `skills-services-secrets.md` |
| Deploy / production operations | `deployment.md` |
| Understand an architectural choice | `decisions.md` → the owning topic register; search `docs/decisions/` for `## D<n>` or a keyword |
| Configure an LLM provider | `interoperability.md` |
| Connect/disconnect an agent (`connect`/`disconnect`/`status`/`sync`/TUI) | `configurator.md` |
| Keep a client aligned with the KB skills | `sync.md` |
| Write or understand a test (unit/smoke/agent) | `testing.md` |

## Documentation maintenance rules

Docs describe the **current state**, not the history: no narration of how we
got here (the *why* lives in `docs/decisions/`, the *how* in the git log) and
no hardcoded counts (link the source of truth). Update the
matching file **in the same session/PR** as the change:

| What changes | File to update |
|---|---|
| New MCP tool or change to its interface | `control-plane.md` §MCP tools |
| Change to the KB model / OKF / filesystem layout | `data-plane.md` |
| Change to transport, auth or scopes | `transport-auth.md` |
| Change to concurrency / git-sync logic | `concurrency.md` |
| New skill, service or secret handling | `skills-services-secrets.md` |
| Client provisioning/sync logic | `sync.md` |
| Client subcommands (`agents`/`connect`/`disconnect`/`status`/`sync`/TUI) or server YAML config | `configurator.md` (client) / `deployment.md` (server config) |
| Token/scope format or enforcement (`kb:<name>:r\|rw`), per-KB git identity or SOPS | `transport-auth.md` (auth/scopes) + `deployment.md` (server config, `KBSpec`) |
| New provisioning `kind` (beyond skill/agent/hook) or per-provider destination | `configurator.md` (client) + `sync.md` (manifest/diff) |
| A feature is **not** implemented (deferred, planned, "future work") | A GitHub issue labelled `enhancement` — never prose in `docs/`. The page keeps only the current limit, with a link to the issue |
| Project status, milestone, task or bug changes | GitHub issue / pull request / release; update `CHANGELOG.md` only through the release workflow |
| Any non-obvious choice (why X and not Y) | One owning topic file under `docs/decisions/` (new D entry); do not duplicate it in other registers |
| New external dependency | Owning topic under `docs/decisions/` (D entry) + `conventions.md` §dependencies |
| New test level or pre-release checklist change | `testing.md` |
| Contributor workflow (PR flow, plan issues, build loop) | `CONTRIBUTING.md` |
| User-facing install/onboarding flow | `getting-started.md` + README |
| Agent-driven install/onboarding flow | `agent-install.md` + README |
