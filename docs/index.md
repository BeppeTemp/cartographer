# Cartographer — documentation index

Entry point for users, agents and contributors. Read this file first, then
only the pages relevant to your task.

## Using Cartographer

| File | Contents |
|---|---|
| [getting-started.md](getting-started.md) | Zero-to-wiki tutorial: install, first KB, connect an agent, first session |
| [agent-install.md](agent-install.md) | Command-by-command runbook for an agent to install Cartographer, mount a first KB, connect, and verify |
| [mcp-clients.md](mcp-clients.md) | Using the server from a host with no Cartographer client side (Open WebUI, a gateway, an SDK agent loop): connect, tokens with an `id`, what the model receives, limits |
| [deployment.md](deployment.md) | Native/k8s topologies, server configuration, health, backup/DR, upgrades and CI/CD |
| [configurator.md](configurator.md) | Multi-provider client and service subcommands (`cartographer setup/agents/connect/disconnect/reconnect/status/sync/doctor/update/kb/service/…` + TUI): flags, generated files per provider, `.cartographer.yaml`, lockfile v2, installation |
| [control-plane.md](control-plane.md) | Go server, complete MCP tool API (source of truth for the list), search index and validation |
| [data-plane.md](data-plane.md) | KB model: Atlas/Map/Journal hierarchy, filesystem layout, expanded concepts, OKF concepts, naming, extended type schema |
| [skills-services-secrets.md](skills-services-secrets.md) | Skill packaging (`SKILL.md`), `type: Service` descriptors, SOPS secrets |
| [sync.md](sync.md) | Client ↔ server provisioning sync: manifest+revision, lockfile, drift detection, layered triggers, prune, path-portability placeholders |
| [use-cases.md](use-cases.md) | Executable patterns for runbooks, imports, source ingest, KB conformance, provider sharing and multi-KB separation |

## Internals & contributing

| File | Contents |
|---|---|
| [overview.md](overview.md) | Current architecture, runtime shapes, principles and component diagram |
| [concurrency.md](concurrency.md) | Single-writer boundary, git sync, `if_match` and conflict registry |
| [transport-auth.md](transport-auth.md) | stdio / HTTP transport, static bearer tokens, per-KB scopes and statelessness |
| [loop.md](loop.md) | Current read, write, validate and lint workflow |
| [interoperability.md](interoperability.md) | OKF boundaries and ownership of provider-specific compatibility documentation |
| [harnesses.md](harnesses.md) | Harness ledger: per agent client, the version last aligned with, where changes are announced, which provider-matrix cells depend on it and the open watch items |
| [decisions.md](decisions.md) | How to refer to and add a decision, plus the generated list of every record under `docs/decisions/` (one file each); decisions explain why, while topic docs define current behavior |
| [conventions.md](conventions.md) | Go conventions (language, style, errors, data-plane safety, tests, dependencies) |
| [testing.md](testing.md) | Deterministic unit, smoke and end-to-end strategy plus release checks |
| [references.md](references.md) | Bibliography (Karpathy, OKF, MCP spec, SOPS, agentskills.io) |
| [brand/index.md](brand/index.md) | Mark, palette tokens, typography and voice; the brand assets live beside it in `docs/brand/` |

## Project state

Mutable state does not live in narrative documentation:

- [open plan issues](https://github.com/BeppeTemp/cartographer/issues?q=is%3Aissue+is%3Aopen+label%3Aplan) — approved implementation handoffs;
- [open bugs](https://github.com/BeppeTemp/cartographer/issues?q=is%3Aissue+is%3Aopen+label%3Abug) — accepted defects;
- [Discussions](https://github.com/BeppeTemp/cartographer/discussions) — ideas, questions and open design;
- [pull requests](https://github.com/BeppeTemp/cartographer/pulls), [releases](https://github.com/BeppeTemp/cartographer/releases) and [`CHANGELOG.md`](https://github.com/BeppeTemp/cartographer/blob/main/CHANGELOG.md) — delivery and completed user-visible work.

## Quick reading paths

| Goal | Read |
|---|---|
| New user | `getting-started.md` → `deployment.md` (for an always-on or shared server) |
| New to the codebase | `overview.md` → `data-plane.md` → `control-plane.md` |
| Add an MCP tool | `control-plane.md` §MCP API → `conventions.md` → `internal/mcpserver/AGENTS.md` (recipe and invariants) |
| Debug concurrency / git conflicts | `concurrency.md` |
| Add a skill or external service | `skills-services-secrets.md` |
| Deploy / production operations | `deployment.md` |
| Understand an architectural choice | `decisions.md` for the generated list, or `ls docs/decisions/D<n>-*` if you already have the number |
| Connect a host with no Cartographer client side (Open WebUI, a gateway, an SDK agent loop) | `mcp-clients.md` |
| Configure an LLM provider | `interoperability.md` |
| Connect/disconnect an agent (`setup`/`connect`/`disconnect`/`reconnect`/`status`/`sync`/`doctor`/TUI) | `configurator.md` |
| Keep a client aligned with the KB skills | `sync.md` |
| Write or understand a test (unit/smoke/agent) | `testing.md` |

## Documentation maintenance rules

Docs describe the **current state**, not the history: no narration of how we
got here (the *why* lives in `docs/decisions/`, the *how* in the git log) and
no hardcoded counts (link the source of truth). Update the
matching file **in the same session/PR** as the change:

| What changes | File to update |
|---|---|
| New MCP tool or change to its interface | `control-plane.md` §MCP API |
| Change to the KB model / OKF / filesystem layout | `data-plane.md` |
| Change to transport, auth or scopes | `transport-auth.md` |
| New or changed lint check | `internal/lint/registry.go` (the one spec), then regenerate the `data-plane.md` §Check catalogue (`CONTRIBUTING.md` §Adding a lint check) |
| Change to concurrency / git-sync logic | `concurrency.md` |
| New skill, service or secret handling | `skills-services-secrets.md` |
| Client provisioning/sync logic | `sync.md` |
| Client or service subcommands (`setup`/`agents`/`connect`/`disconnect`/`reconnect`/`status`/`sync`/`doctor`/`service`/TUI…) or server YAML config | `configurator.md` (client) / `deployment.md` (server config) |
| Token/scope format or enforcement (`kb:<name>:r\|rw`), git identity (author, committer, `Cartographer-Token`) or SOPS | `transport-auth.md` (auth/scopes) + `deployment.md` (server config, `KBSpec`) |
| New provisioning `kind` (beyond skill/agent/hook) or per-provider destination | `configurator.md` (client) + `sync.md` (manifest/diff) + its `KindInfo` in `provisioning.Kinds` (the Atlas lists it; a test fails without it, D864) |
| A feature is **not** implemented (deferred, planned, "future work") | A Discussion while it is an idea, a `plan` issue once accepted (`CONTRIBUTING.md` §Issues: bug, plan) — never prose in `docs/`. The page keeps only the current limit, with a link to the issue |
| Project status, milestone, task or bug changes | GitHub issue / pull request / release; update `CHANGELOG.md` only through the release workflow (which groups the newest section by area, D865) |
| An architectural or contract choice (why X and not Y) | One new file `docs/decisions/D<n>-<slug>.md`, `<n>` = the plan issue or PR number (D672; `make decisions-new`; the release PR regenerates `docs/decisions.md`, D328); never a second entry for the same choice |
| New external dependency | A decision file under `docs/decisions/` + `conventions.md` §dependencies |
| New test level or pre-release checklist change | `testing.md` |
| Which CI jobs a PR runs (`web`, `windows`, `demo-edge` path filters) | the table in `scripts/ci-filters.sh` and its test `internal/repodocs/ci_filters_test.go` (D994), then `testing.md` §Go tests; never regex inline in `ci.yml` |
| The read tools, or `demo/astronomy` | They reach the live MCP demo (`deploy/demo/`, `deployment.md` §The hosted MCP demo) at the next release, when `demo.yml` rebuilds it; nothing to update by hand (D751) |
| `demo/guestbook/`, `deploy/demo/worker.js` (moderation) or the demo's Terraform | The seed reaches the live guestbook only through its private remote, the Worker and infrastructure only through a maintainer `terraform apply`; `deploy/demo/README.md`, `deployment.md` §The hosted MCP demo, `make demo-worker-test`, `make demo-smoke` (D800) |
| Atlas UI (`web/`) or its routes/config | `make web` to rebuild the committed bundle; `deployment.md` §The embedded Atlas UI; `testing.md` §Frontend; nothing to run for the README's hero: the release that changes `web/` re-records it (D790), and a new step of the tour goes in `hero.spec.ts`; a new API request the UI makes needs its line in `demo/export.mjs`, or the public demo answers it with a 404 (D745) |
| The README's launch film (`film/`) or the story it tells | nothing to run: the release candidate (`preview.yml`, job `film`, `film.yml`) re-renders it when the hash of its inputs (`film/inputs.sh`) differs from the `Film-Tree:` trailer on `readme-film`, which the README embeds (D807, D1068); a render failure blocks the candidate and so the train, which is how a camera change in `web/src/lib/graph3d/scene.ts` that breaks it is caught; an import added to `film/src` goes in `film/inputs.sh`; preview locally with `film/README.md` |
| `test/macos/` (the launchd, `install.sh` and Cask scripts of the `test-macos` job), `internal/service/`, `install.sh` or `.goreleaser.yaml` | `testing.md` §Real macOS; the `test-macos` job in `ci.yml` and the `macos` filter in `scripts/ci-filters.sh` (D997); the scripts refuse to run outside CI because they register a real launchd job |
| `test/forge/` (the shared `pre-receive.sh` hook, `run.sh` against Forgejo and GitLab CE), `internal/gitx/gitxtest` or the git config `test/e2e/run.sh` and `test/smoke/http.sh` isolate | `testing.md` §Go tests (git write coverage) and §Real forges; the `forge` job in `ci.yml` and the `forge` filter in `scripts/ci-filters.sh` (D996); pin bumps of the container images live in `run.sh` |
| `Dockerfile`, `.dockerignore`, `go.mod`/`go.sum` or `deploy/demo/` (what the image is built from) | `test/smoke/image.sh` still passes on the built image (`make docker-smoke`); `testing.md` §Image smoke, `deployment.md` §CI/CD (the `docker` job and the candidate smoke, D995); the `docker` filter in `scripts/ci-filters.sh` |
| Contributor workflow (PR flow, plan issues, build loop) | `CONTRIBUTING.md` |
| What an agent client reads (instruction file, skill/agent/hook/MCP paths) | The provider matrix **first** (`destinationMatrix` in `internal/provisioning/provisioning.go`, `projectDestinationMatrix` in `internal/provisioning/workspacescope.go`, provider descriptors in `internal/configurator/registry.go`), then `docs/harnesses.md` and `docs/interoperability.md`, which restate it |
| A client re-audit (version bump, new sources, watch items) | `harnesses.md` (via the `cartographer-audit-clients` skill); the matrix and docs above only if a cell changes |
| User-facing install/onboarding flow | `getting-started.md` (+ README Quick start/Install, kept short: details live in `getting-started.md`) |
| What a connection with no Cartographer client receives (the `initialize` guide, `kb_guide`, the surface shaped by the token, the limits) | `mcp-clients.md` (`control-plane.md` / `transport-auth.md` own the mechanics; scenario `24_generic_mcp_client` is the executable form) |
| Agent-driven install/onboarding flow | `agent-install.md` + README |
| Logo, banner, palette or typography | `brand/` assets + tokens, then `brand/index.md` |
