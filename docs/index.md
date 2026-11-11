# Cartographer — documentation index

Entry point for users, agents and contributors. Read this file first, then
only the pages relevant to your task.

## Using Cartographer

| File | Contents |
|---|---|
| [getting-started.md](getting-started.md) | Zero-to-wiki tutorial: install, first KB, connect an agent, first session |
| [faq.md](faq.md) | Questions about concurrent writes, `sync` and how Cartographer compares with an instruction file, Notion or Obsidian |
| [agent-install.md](agent-install.md) | Command-by-command runbook for an agent to install Cartographer, mount a first KB, connect, and verify |
| [mcp-clients.md](mcp-clients.md) | Using Meridian from a host with no Cartographer client side (Open WebUI, a gateway, an SDK agent loop): connect, tokens with an `id`, what the model receives, limits |
| [deployment.md](deployment.md) | Deployment components, deployment scenarios and recipes (native, stdio, single VM, Kubernetes, several servers, the Helm chart), server configuration, health, backup/DR, upgrades, CI/CD and the Surveyor (a third role) |
| [surveyor-recipes.md](surveyor-recipes.md) | Surveyor exec recipes: the `recipes.<name>` keys, protocol v1 (stdin request, JSON-lines listing), failures, trust model, example script |
| [configurator.md](configurator.md) | Compass (the multi-provider client) and service subcommands (`cartographer setup/agents/connect/disconnect/reconnect/status/sync/call/doctor/update/kb/service/…`): flags, generated files per provider, `.cartographer.yaml`, lockfile v2, installation |
| [control-plane.md](control-plane.md) | Go server, complete MCP tool API (source of truth for the list), search index and validation |
| [data-plane.md](data-plane.md) | KB model: Atlas/Map/Journal hierarchy, filesystem layout, expanded concepts, OKF concepts, naming, extended type schema |
| [skills-services-secrets.md](skills-services-secrets.md) | Skill packaging (`SKILL.md`), `type: Service` descriptors, SOPS secrets |
| [sync.md](sync.md) | Client ↔ server provisioning sync: manifest+revision, lockfile, drift detection, layered triggers, prune, path-portability placeholders |
| [plugins.md](plugins.md) | Plugins: a git repository or folder that adds clients, skills, subagents, hooks, commands and MCP tools; manifest, approval per version, the Starlark script model and its permissions, a worked example |
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

- [open bugs](https://github.com/BeppeTemp/cartographer/issues?q=is%3Aissue+is%3Aopen+label%3Abug) — accepted defects;
- [open ideas](https://github.com/BeppeTemp/cartographer/issues?q=is%3Aissue+is%3Aopen+label%3Aidea) — proposals not accepted yet;
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
| Choose how to deploy | `deployment.md` §Components and §Deployment scenarios; Compass in `configurator.md` §Headless hosts, CI and agent containers |
| Understand an architectural choice | `decisions.md` for the generated list, or `ls docs/decisions/D<n>-*` if you already have the number |
| Connect a host with no Cartographer client side (Open WebUI, a gateway, an SDK agent loop) | `mcp-clients.md` |
| Configure an LLM provider | `interoperability.md` |
| Connect/disconnect an agent (`setup`/`connect`/`disconnect`/`reconnect`/`status`/`sync`/`call`/`doctor`) | `configurator.md` |
| Keep a client aligned with the KB skills | `sync.md` |
| Install, approve or write a plugin (a client, a command, an MCP tool, a sync handler) | `plugins.md` |
| Write or understand a test (unit/smoke/agent) | `testing.md` |

## Documentation maintenance rules

Docs describe the **current state**, not the history: no narration of how we
got here (the *why* lives in `docs/decisions/`, the *how* in the git log) and
no hardcoded counts (link the source of truth). A gate checks the narrowest mechanical part of this rule (D1128); its allow-list lives in `internal/repodocs/doc_rot_test.go`. Update the
matching file **in the same session/PR** as the change. The path-mappable rows are
mirrored in `scripts/doc-owners.tsv`, which `scripts/pr-triage.sh` reads to warn about a
page that was not touched; the table and the file change together, and a test
(`internal/repodocs`) fails when a mapped page is not in the table:

| What changes | File to update |
|---|---|
| New MCP tool or change to its interface | `control-plane.md` §MCP API |
| A public API route, its tier or `docs/openapi.json` (`internal/mcpserver/apiroutes.go`) | `control-plane.md` §The public API document + `make openapi` |
| Change to the KB model / OKF / filesystem layout | `data-plane.md` |
| Change to transport, auth or scopes | `transport-auth.md` |
| New or changed lint check | `internal/lint/registry.go` (the one spec), then regenerate the `data-plane.md` §Check catalogue (`CONTRIBUTING.md` §Adding a lint check) |
| Change to concurrency / git-sync logic | `concurrency.md` |
| New skill, service or secret handling | `skills-services-secrets.md` |
| Client provisioning/sync logic | `sync.md` |
| Plugin manifest, contribution points or store (`internal/plugin`) | `plugins.md` |
| Client or service subcommands (`setup`/`agents`/`connect`/`disconnect`/`reconnect`/`status`/`sync`/`call`/`doctor`/`service`…) or server YAML config | `configurator.md` (Compass) / `deployment.md` (Meridian config) |
| Token/scope format or enforcement (`kb:<name>:r\|rw`), git identity (author, committer, `Cartographer-Token`) or SOPS | `transport-auth.md` (auth/scopes) + `deployment.md` (server config, `KBSpec`) |
| New provisioning `kind` (beyond skill/agent/hook) or per-provider destination | `configurator.md` (client) + `sync.md` (manifest/diff) + its `KindInfo` in `contract.Kinds` (the Atlas lists it; a test fails without it, D864) |
| A feature is **not** implemented (deferred, planned, "future work") | An `idea` issue while it is a proposal, `accepted` once triaged (`CONTRIBUTING.md` §Issues: bug, idea) — never prose in `docs/`. The page keeps only the current limit, with a link to the issue |
| Project status, task or bug changes | GitHub issue / pull request / release; update `CHANGELOG.md` only through the release workflow (which groups the newest section by area, D865) |
| An architectural or contract choice (why X and not Y) | One new file `docs/decisions/D<n>-<slug>.md`, `<n>` = the PR number (D672; `make decisions-new`; a maintainer's queued change writes `D-new-<slug>.md` and the lander numbers it, D1706; the release PR regenerates `docs/decisions.md`, D328); never a second entry for the same choice |
| A new user-visible name of a component, or a sentence that names one | The four components are Meridian (the server), Surveyor, Atlas and Compass (the client), always in that order: say Meridian or Compass when the sentence names the component, and keep "MCP server", third-party servers, agent clients and MCP/HTTP clients, command names, config keys, images and routes as they are. A new name goes in `deployment.md` §Components, the Atlas labels, the CLI help and the Helm chart in the same change (D1867) |
| New external dependency | A decision file under `docs/decisions/` + `conventions.md` §dependencies |
| New test level or pre-release checklist change | `testing.md` |
| Which CI jobs a PR runs (`web`, `windows`, `demo-edge` path filters) | the table in `scripts/ci-filters.sh` and its test `internal/repodocs/ci_filters_test.go` (D994), then `testing.md` §Go tests; never regex inline in `ci.yml` |
| The read tools, or `demo/astronomy` | They reach the live MCP demo (`deploy/demo/`, `deployment.md` §The hosted MCP demo) at the next release, when `demo.yml` rebuilds it; nothing to update by hand (D751) |
| `deploy/helm/**`, `test/helm/` or `scripts/install-helm.sh` (the Helm chart) | `deploy/helm/cartographer/README.md` (what `helm show readme` prints), `deployment.md` §Example: K8s pod and the Surveyor sections, `testing.md` §Helm chart, `make helm-check`, the `helm` filter in `scripts/ci-filters.sh`; the chart's version and image tag are stamped by the release workflows, never by hand (D1441) |
| `demo/guestbook/`, `deploy/demo/worker.js` (moderation) or the demo's Terraform | The seed reaches the live guestbook only through its private remote, the Worker and infrastructure only through a maintainer `terraform apply`; `deploy/demo/README.md`, `deployment.md` §The hosted MCP demo, `make demo-worker-test`, `make demo-smoke` (D800) |
| Atlas UI (`web/`) or its routes/config | `make web` to rebuild the committed bundle; `deployment.md` §The embedded Atlas UI; `testing.md` §Frontend; nothing to run for the README's hero: the release that changes `web/` re-records it (D790), and a new step of the tour goes in `hero.spec.ts`; a new API request the UI makes needs its line in `demo/export.mjs`, or the public demo answers it with a 404 (D745) |
| The README's launch film (`film/`) or the story it tells | nothing to run: the release candidate (`preview.yml`, job `film`, `film.yml`) re-renders it when the hash of its inputs (`film/inputs.sh`) differs from the `Film-Tree:` trailer on `readme-film`, which the README embeds (D807, D1068); a render failure blocks the candidate and so the train, which is how a camera change in `web/src/lib/graph3d/scene.ts` that breaks it is caught; an import added to `film/src` goes in `film/inputs.sh`; preview locally with `film/README.md` |
| `test/linux/` (the containers-without-systemd scripts of the `test-linux-nosystemd` job) | `testing.md` §Real Linux without systemd; the `test-linux-nosystemd` job in `ci.yml` (D1682); the images are pinned by digest |
| `test/macos/` (the launchd, `install.sh` and Cask scripts of the `test-macos` job), `internal/service/`, `install.sh` or `.goreleaser.yaml` | `testing.md` §Real macOS; the `test-macos` job in `ci.yml` and the `macos` filter in `scripts/ci-filters.sh` (D997); the scripts refuse to run outside CI because they register a real launchd job |
| `test/forge/` (the shared `pre-receive.sh` hook, `run.sh` against Forgejo and GitLab CE), `internal/gitx/gitxtest` or the git config `test/e2e/run.sh` and `test/smoke/http.sh` isolate | `testing.md` §Go tests (git write coverage) and §Real forges; the `forge` job in `ci.yml` and the `forge` filter in `scripts/ci-filters.sh` (D996); pin bumps of the container images live in `run.sh` |
| `Dockerfile`, `.dockerignore`, `go.mod`/`go.sum` or `deploy/demo/` (what the image is built from) | `test/smoke/image.sh` and `test/smoke/surveyor-image.sh` still pass on the built images (`make docker-smoke`, `make docker-surveyor-smoke`); `testing.md` §Image smoke, the recipes of `deployment.md` §Example: single VM and §Example: a derived image for exec recipes (they assume the image's entrypoint and non-root user, which the smoke test asserts), `deployment.md` §CI/CD (the `docker` job and the candidate smoke, D995); the `docker` filter in `scripts/ci-filters.sh` |
| Contributor workflow (PR flow, plans, build loop) | `CONTRIBUTING.md` |
| What an agent client reads (instruction file, skill/agent/hook/MCP paths) | The provider matrix **first** (`DestinationMatrix` in `internal/provisioning/contract/provisioning.go`, `ProjectDestinationMatrix` in `internal/provisioning/contract/workspacescope.go`, provider descriptors in `internal/configurator/registry.go`), then `docs/harnesses.md` and `docs/interoperability.md`, which restate it |
| A client re-audit (version bump, new sources, watch items) | `harnesses.md` (via the `cartographer-audit-clients` skill); the matrix and docs above only if a cell changes |
| User-facing install/onboarding flow | `getting-started.md` (+ README Quick start/Install, kept short: details live in `getting-started.md`) |
| What a connection with no Cartographer client receives (the `initialize` guide, `kb_guide`, the surface shaped by the token, the limits) | `mcp-clients.md` (`control-plane.md` / `transport-auth.md` own the mechanics; scenario `24_generic_mcp_client` is the executable form) |
| Agent-driven install/onboarding flow | `agent-install.md` + README |
| Logo, banner, palette or typography | `brand/` assets + tokens, then `brand/index.md` |
