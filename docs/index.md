# Cartographer — documentation index

Entry point for users, agents and contributors. Read this file first, then
only the pages relevant to your task.

## Using Cartographer

| File | Contents |
|---|---|
| [getting-started.md](getting-started.md) | Zero-to-wiki tutorial: install, first KB, connect an agent, first session |
| [deployment.md](deployment.md) | Topologies (native local service / k8s / multi-server), server config, observability, backup/DR, crash recovery, upgrades, CI/CD |
| [configurator.md](configurator.md) | Multi-provider client (`cartographer agents/connect/disconnect/status/sync` + TUI): flags, generated files per provider, `.cartographer.yaml`, lockfile v2, installation |
| [control-plane.md](control-plane.md) | Go server, complete MCP tool API (source of truth for the list), search index, validation and provenance |
| [data-plane.md](data-plane.md) | KB model: Atlas/Map/Journal hierarchy, filesystem layout, expanded concepts, OKF concepts, naming, extended type schema |
| [skills-services-secrets.md](skills-services-secrets.md) | Skill packaging (`SKILL.md`), `type: Service` descriptors, SOPS secrets |
| [sync.md](sync.md) | Client ↔ server provisioning sync: manifest+revision, lockfile, drift detection, layered triggers, prune, path-portability placeholders |
| [use-cases.md](use-cases.md) | 5 real scenarios: incident, runbook, research, multi-provider, enterprise multi-KB |

## Internals & contributing

| File | Contents |
|---|---|
| [overview.md](overview.md) | Vision, guiding principles, the two product profiles, general diagram, tech stack |
| [concurrency.md](concurrency.md) | Single-writer model, git sync, `if_match`, `needs-resolution` state machine |
| [transport-auth.md](transport-auth.md) | stdio / Streamable HTTP transports, OAuth 2.1, per-KB scopes, statelessness |
| [loop.md](loop.md) | Operating loop: supervised ingest, query, lint (scoped / full / deep), costs and capacity |
| [interoperability.md](interoperability.md) | OKF compliance, multi-provider configurator, capability matrix (Claude/Codex/Kiro/OpenCode) |
| [decisions.md](decisions.md) | Decision register: closed architectural (AD) and implementation (D) entries. Each entry = decision + reason + pointer; history lives in the git log |
| [conventions.md](conventions.md) | Go conventions (language, style, errors, data-plane safety, tests, dependencies) |
| [roadmap.md](roadmap.md) | **Project status** (the only place it lives) and milestones, completed and future |
| [testing.md](testing.md) | Test strategy: levels (unit/smoke/HTTP/agent-level), operator-vs-agent distinction, pre-release checklist |
| [references.md](references.md) | Bibliography (Karpathy, OKF, MCP spec, SOPS, agentskills.io) |

## Quick reading paths

| Goal | Read |
|---|---|
| New user | `getting-started.md` → `deployment.md` (when you outgrow Local Core) |
| New to the codebase | `overview.md` → `data-plane.md` → `control-plane.md` |
| Add an MCP tool | `control-plane.md` §tools → `conventions.md` → `CLAUDE.md` (recipe) |
| Debug concurrency / git conflicts | `concurrency.md` |
| Add a skill or external service | `skills-services-secrets.md` |
| Deploy / production operations | `deployment.md` |
| Understand an architectural choice | `decisions.md` — **do not read it whole**: grep the entry (`## D<n>` or keyword) and read only that; the quick index at the top maps area → entry |
| Configure an LLM provider | `interoperability.md` |
| Connect/disconnect an agent (`connect`/`disconnect`/`status`/`sync`/TUI) | `configurator.md` |
| Keep a client aligned with the KB skills | `sync.md` |
| Write or understand a test (unit/smoke/agent) | `testing.md` |

## Documentation maintenance rules

Docs describe the **current state**, not the history: no narration of how we
got here (the *why* lives in `decisions.md` in compact form, the *how* in the
git log) and no hardcoded counts (link the source of truth). Update the
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
| Milestone completed or task dropped | `roadmap.md` (never in `CLAUDE.md`: it is stable imprinting) |
| Any non-obvious choice (why X and not Y) | `decisions.md` (new D entry) |
| New external dependency | `decisions.md` (D entry) + `conventions.md` §dependencies |
| New test level or pre-release checklist change | `testing.md` |
| User-facing install/onboarding flow | `getting-started.md` + README |
