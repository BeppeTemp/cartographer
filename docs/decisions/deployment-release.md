# Deployment and release decisions

Server configuration, bootstrap, local service, release automation and upgrade visibility. Current behavior: [`../deployment.md`](../deployment.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d21"></a>
## D21 — Configuration via environment variables
All CLI options have a corresponding env var (`CARTOGRAPHER_KB`, `CARTOGRAPHER_HTTP`, `CARTOGRAPHER_TOKENS`). The CLI flag takes precedence over the env var. `CARTOGRAPHER_AUTH` is env-var-only with three states: `true` (requires auth, fatal if no tokens), `false` (disables), unset (auto — enabled if tokens are present). Resolution happens upfront in `main()` via `envFallback(flag, envKey)`. No changes to `internal/auth`.

---

---

<a id="d38"></a>
## D38 — Server configuration via YAML (`internal/config`, `gopkg.in/yaml.v3`)

**Decision.** `cartographer serve --config file.yaml` (or `CARTOGRAPHER_CONFIG`) loads
`internal/config.Config` with precedence **CLI flags > env > YAML > default**; `kbs: []` accumulates
across levels, scalar fields follow the standard precedence. New direct dependency
`gopkg.in/yaml.v3` (infrastructure config does not have `internal/okf`'s stdlib-only constraint).
**Rationale.** Flags/env do not scale beyond a few scalar options: `kbs: []` with mixed
`remote`/`path` and the git/audit config require nested structure. YAML is consistent with
`config.example.yaml` mounted from a ConfigMap; the flag>env>YAML precedence preserves full
backward compatibility with pure flag/env usage.
Details: `docs/deployment.md` §Configuration: flags, env, YAML.

---

<a id="d39"></a>
## D39 — Server-side KB bootstrap from git remote (replaces the k8s init container)

**Decision.** `cartographer serve` clones each `kbs: [{remote: ...}]`/`CARTOGRAPHER_KB_REMOTES` into
`<data>/<nome>` before opening the KBs, only if the destination does not already exist; `GIT_SSH_COMMAND` is
built from `git.ssh_key`/`git.known_hosts` if not already present in the environment (env always wins).
**Rationale.** The previous k8s deployment required a separate init container for the initial
clone — one more failure point, no sharing with the existing fetch/pull-rebase path.
Bringing it into the `serve` process removes the init container; "skip if already present" avoids
re-clones at every pod restart.
Details: `docs/deployment.md` §KB bootstrap from git remote.

---

<a id="d41"></a>
## D41 — Direct release+deploy pipeline (least-privilege SA) + `install.sh` without Homebrew

**Decision.** `.gitea/workflows/build-deploy.yaml` runs on `v*` tags: vet+test → build+push
Docker image → cross-compile client (`darwin/linux` × `amd64/arm64`) + checksums → Gitea
release → tag bump in `homelab-manifests` → `kubectl apply`+`rollout status` with a kubeconfig of
a least-privilege service account (get/patch on the cartographer `Deployment` only). Client
installation via `install.sh` (POSIX `sh`, detects OS/arch, downloads from the latest release, verifies
checksums).
**Rationale.** A direct deploy (without ArgoCD/Flux) is proportionate to the homelab's scale and
limits the blast radius of a compromised CI token to a single Deployment. `install.sh` without
Homebrew avoids maintaining a formula/tap for a project with limited distribution.
Details: `docs/deployment.md` §CI/CD: release + deploy pipeline, §Client installation.

---

<a id="d53"></a>
## D53 — Explicit KB name (`kbs[].name`) + per-KB conventions for the git token (`git.token_dir`) and SOPS key (`sops.age_key_dir`)

**Context.** With multiple remote KBs in `kbs[]`, the KB name was always derived from remote/path (no
way to set it explicitly); git authentication on private HTTPS required the token
in the remote URL (ends up in cleartext in `.git/config`) or out-of-band handling.

**Decision.** `KBSpec.Name` (yaml `name`), if set, wins over derivation wherever the name is
used (endpoint, token scopes, clone dir). `GitConfig.TokenDir`: if `<token_dir>/<name>.token`
exists, it is used as the HTTPS credential injected via `credential.helper` in the per-process
environment — the token never touches argv/URL/`.git/config`. `SopsConfig.AgeKeyDir`: fallback
`KBSpec.SopsAgeKeyFile` > `<age_key_dir>/<name>.age` > global.
**Rationale.** Convention over configuration: with a fixed KB name, the token and age key are
found by convention instead of being listed field by field — a single secret/volume to
mount for all KBs, with an explicit override always available.
**Discarded alternatives.** Token in the remote URL (ends up in cleartext in logs/`.git/config`);
checking the remote's scheme in Go before injecting the helper (complexity for zero gain).
Details: `docs/deployment.md` §Environment variables.

---

<a id="d68"></a>
## D68 — Deploy via Flux GitOps, `kubectl apply` removed from CI

**Context.** The `build-deploy.yaml` pipeline ended with a "Deploy to k3s" step
(`kubectl apply` + `rollout status`) using a long-lived kubeconfig (`secrets.KUBECONFIG_B64`,
service account `gitea-deployer`). But the cluster is GitOps: the Flux Kustomization
`flux-system/ai-tools` already reconciles `homelab-manifests` and applies the manifest bump made
by the previous step. The v1.5.0 release (run #41) made it evident: the imperative step
failed (`kubectl apply` → *"the server has asked for the client to provide credentials"*, the SA's
token no longer valid for the openapi download), marking the run **red**, while the real deploy
had already succeeded **via Flux**.

**Decision.** Removed the "Deploy to k3s" step: the pipeline stops at the manifest bump
(build → push image → release → commit to `homelab-manifests`); the deploy is delegated
entirely to Flux. This eliminates the redundancy and the conflict with Flux's drift control
(two actors applying the same Deployment), and removes the long-lived `KUBECONFIG_B64`
kubeconfig from the repo secrets (one less surface). **Discarded alternative:** regenerating the
`gitea-deployer` token — it would only have restored a deploy path that has no reason
to exist alongside Flux.

---

<a id="d73"></a>
## D73 — Local mode as a native service (`cartographer service`), Docker out of the local deploy

**Status: implemented (2026-07-09).**

**Decision.** The local deploy mode is the native binary daemonized as a **user
service** — LaunchAgent on macOS, systemd user unit on Linux — managed by the new subcommand
`cartographer service install|uninstall|start|stop|restart|status` (`internal/service` +
`cmd/cartographer/service.go`). `docker-compose.yml` is removed: the Docker image remains only
as a CI artifact for the k8s deploy, no longer a documented topology.

**Why.** (a) Containers on macOS are expensive exactly where Cartographer is most sensitive:
slow virtualized filesystem for git working trees and SQLite, always-resident VM, fragile bind
mounts — for a service that is a single static Go binary, the container added
nothing. (b) The "local Docker" topology was already just `serve --http --data` on a trusted network: the
native service is the same configuration without the layer in between. (c) A **single daemon**
per machine (instead of stdio spawned per-agent) preserves the single-owner invariant on the
working tree, per-KB locks (in-process mutex) and the SQLite index, and serves all the
configurator's providers (HTTP-only) without changing it.

**Multi-server cooperation (design clarification).** Multiple instances — local and/or k8s — mount
the same KB from the same git remote: git is the synchronization fabric (pull-rebase →
commit → push at every write), concurrent writes on the same concept degrade into
rebase-conflict/`needs-resolution` (expected behavior). Client-side constraint: one client
per machine, pointed at **one server at a time**.

**Attached choices.**
- `serve` over HTTP with `data:` configured but **empty** starts with 0 KBs (warning, `/health`
  active) instead of exiting: avoids the `KeepAlive` crash-loop on a fresh machine right
  after `service install`. Fail-fast remains for stdio and for no KB source configured.
- Default bind `127.0.0.1:8080`: loopback ⇒ auth auto-off with no network exposure.
- `service install` is idempotent and never touches an existing YAML config (`--data`/`--http`
  ignored with a warning: edit the file + `service restart`).
- `install.sh update` restarts the service **only if running** (`status` exit 0):
  a deliberately stopped service stays stopped, and `launchctl kickstart` on a job not
  loaded would fail anyway (systemctl-like exit codes: 0 running / 3 stopped / 4 absent).
- Sugar in `connect`: failed probe + loopback URL + service not active ⇒ offer of
  install+start with polling on `/health` (interactive) or a hint on stderr (non-interactive).

---

<a id="d83"></a>
## D83 — Service install robustness: create the data dir, tolerate its absence, stable plist binary path

**Status: implemented (2026-07-23).**

**Context.** `service install` (D73) wrote a config pointing at `~/cartographer-data` but never created that
directory, and `serve`'s `discoverKBPaths` (`cmd/cartographer/serve.go`) called `os.ReadDir` on it unconditionally,
`log.Fatalf`-ing when the dir was missing — a fresh `brew install` produced a launchd `KeepAlive` crash-loop.
Separately, the plist embedded the `filepath.EvalSymlinks`-resolved binary path, i.e. the **versioned** Homebrew
Caskroom path (`/opt/homebrew/Caskroom/cartographer/<ver>/…`): every `brew upgrade` removed that path and broke the
service until `service install` was re-run.

**Decision.**

a) **`Manager.Install`** (`internal/service/manager.go`) now `os.MkdirAll`s the data dir (the value from a
freshly-generated config, or read back from an already-existing one) right after resolving the config path, and
logs `created <dir>` to stderr when it does.

b) **`discoverKBPaths`** (`cmd/cartographer/serve.go`) treats a missing data dir like an empty one: on
`os.IsNotExist` it creates the dir, logs to stderr, and returns an empty slice instead of failing startup. The
existing `log.Fatalf` at the call site still fires for other errors (permissions, not-a-directory).

c) **The plist/unit binary path** (`internal/service/paths.go`, `resolveStableBinPath`) is the as-invoked
`os.Executable()` path, no longer `EvalSymlinks`-resolved — except that a stable Homebrew symlink
(`/opt/homebrew/bin/cartographer` or `/usr/local/bin/cartographer`) is preferred over it when the symlink resolves
to the same file, so the recorded path survives `brew upgrade`. `Manager.Status` uses the same resolution so it
reports the configured path.

**Rationale.** All three are the same invariant from different angles: *install must never produce a non-bootable
state, and serve must treat a missing data dir exactly like an empty one.* This **amends D73's assumption** that
the data dir exists once the config is written — D73 correctly specified the empty-dir behavior (0 KB, `/health`
up) but implicitly assumed someone had created the directory; D83 makes that explicit and enforced at both ends
(install creates it, serve tolerates its absence either way).

---

<a id="d84"></a>
## D84 — Readiness signal and per-KB path routing

**Status: implemented (2026-07-23).**

**Context.** Follow-up of D73's "0 KBs, `/health` active" attached choice: `/health` always returns
`status:"ok"`, even with 0 KBs mounted, while the functional endpoint 400s (`kb parameter required`
with no `?kb=` and more than one KB, or zero). No liveness/readiness distinction — agents and
`connect` see a "healthy" server that is unusable. Separately, `/mcp/<name>` (implied by `kbs[].name`
as "the name used everywhere", D53) was probed by a real user and not implemented: bare `/mcp`
auto-routes only with exactly 1 KB, `?kb=` works, any other path 404s.

**Decision.**
- **WP1 — path routing.** `MultiKBServer.Handler` recognizes `/mcp/<name>` alongside the existing
  bare `/mcp` (single-KB auto-route) and `/mcp?kb=<name>`. Unknown name → `404 unknown kb`. If both
  the path and `?kb=` are present and name different KBs → `400 conflicting kb selection` (explicit
  over silent precedence).
- **WP2 — readiness.** `/health` (single-KB and MultiKB) gains `ready: <bool>` (single-KB: always
  `true`; MultiKB: `len(kbs) > 0`); `status` stays `"ok"` unconditionally — readiness is additive,
  it must never change liveness semantics for existing probes. New `/ready` endpoint on both
  handlers: `200 {"ready":true}` / `503 {"ready":false,"kbs":0}`.

**Rationale.** Keeping `/health` as pure liveness (never fails from a KB-mounting issue) avoids
turning a "no KBs mounted yet" state into a restart-loop on k8s if `livenessProbe` were pointed at
it; `/ready` gives `readinessProbe` a signal that actually reflects usability. Path routing closes
the gap between what D53 already documented as "the name used everywhere" and what the HTTP layer
actually accepted.

---

<a id="d85"></a>
## D85 — `kb create` and first-KB onboarding: a CLI command, not only the agentic skill

**Status: implemented (2026-07-24).**

**Context.** With an empty data dir the server mounts 0 KBs and `/mcp` 400s, but nothing guided a
first-time user to create one: the CLI dispatch had no `kb` subcommand — creation existed only as
the agentic `kb-create` skill (Gitea-repo-first, operator-only), and neither `service install` nor
`connect` offered a hint. The happy path (`brew install` → `service install` → connect) had no step
in between to actually get a KB onto disk.

**Decision.**
- **WP1 — `cartographer kb create <name>` (`cmd/cartographer/kbcmd.go`).** Scaffolds
  `<data>/<name>` via the exact `kb.Init` bootstrap `serve --kb <path> --init` already uses (git
  init + OKF layout) — no second scaffold implementation. Data dir resolution mirrors `service
  install`'s: the local service's config YAML `data:` field, else `~/cartographer-data`; `--data`
  overrides. Name validated as directory-safe (`^[A-Za-z0-9_-]+$` — no existing validator to reuse,
  none existed before this).
- **WP2 — guidance.** After a successful create, `kb create` probes the local server's `/health`
  (base URL: the service config's `http:` if present, else the way `connect` derives it —
  `.cartographer.yaml` `server_url`/localhost default, `/mcp` stripped) and, if reachable, prints
  the `service restart` hint (or does it and waits healthy, with `--restart`). `service install`
  probes the same way after installing and, if `/health` reports 0 KBs mounted, prints a hint
  pointing at `kb create`. Both parse `/health` defensively: the `ready`/`kbs` fields are D84
  additions, absent on an older server — `kbs` absent falls back to checking the data dir directly.
- **WP3 — narrative.** README's quick start and `docs/deployment.md`'s native-service example both
  lead with the 4-command path (`brew install` → `service install` → `kb create <name>` →
  `connect`); `serve --kb <path> --init` remains documented as the stdio/dev path.

**Rationale.** A CLI command belongs on every machine that already has the binary, works without
Gitea/a git remote, and matches the plain local-service topology (no persistence concerns — the
data dir itself is the persistence layer, unlike the k8s/GitOps topology the `kb-create` skill
targets). The skill remains the right tool for that GitOps case (per-KB Gitea repo, service user,
ConfigMap `kbs:` entry): `kb create` doesn't replace it, it covers the case the skill doesn't —
a single local/native-service machine with no remote yet.

---

<a id="d95"></a>
## D95 — Upgrade transparency through version-skew hints

**Status: implemented (2026-07-24).**

**Decision.** `cartographer status` reports the client and server versions before its provisioning-artifact result. A non-`dev` version mismatch is advisory and leaves the existing status exit codes unchanged; for a loopback server with an installed native service, the warning includes the explicit `cartographer service restart` command. Servers that predate the health version field remain compatible and simply produce no skew warning.

**Rationale.** A Homebrew upgrade replaces the binary at the stable path, but cannot safely replace a process already executing it. Automatically restarting from a cask hook could interrupt an in-flight write and bypass the operator's drain decision, while an explicit, contextual hint makes the necessary restart visible. The same report makes a client ahead of a Kubernetes image rollout observable without inventing a separate drift state.

---

<a id="d97"></a>
## D97 — Agent-driven onboarding mounts remotes through `kb clone`

**Decision.** `cartographer kb clone <remote> [name]` is the local-service entry point for an
existing first KB. It derives and validates the destination name, clones only into the managed data
directory, validates the result as OKF, and removes a failed or invalid clone. The stable,
agent-addressed `docs/agent-install.md` runbook covers installation, service setup, mounting,
provider connection, and verification; README exposes it as a copy-paste prompt template.

**Rationale.** A hand-run `git clone` cannot enforce the data-directory, name, cleanup, and OKF
invariants that make a directory mountable by the service. Keeping the bootstrap runbook separate
from the human tutorial gives an agent a raw-URL-safe, imperative procedure before Cartographer or
its provisioned `cartographer-ops` skill exists locally.

---

<a id="d112"></a>
## D112 — Reserved local endpoint defaults

**Status: implemented (2026-07-28).**

**Decision.** New local service configurations listen on `127.0.0.1:39273`,
and a new client uses `http://localhost:39273/mcp`. The dependency-light
`internal/defaults` package owns the port, listen address, and MCP URL so the
native-service and client first-run paths cannot drift. `cartographer serve`
without an explicit HTTP setting remains stdio.

**Compatibility.** Existing server YAML and `.cartographer.yaml` values,
including an explicit port 8080, remain authoritative and are never rewritten.
The normal precedence rules continue to apply: server flag > environment >
YAML > default; client existing YAML > `CARTOGRAPHER_SERVER_URL` > local
default.

**Rationale.** Port 8080 is frequently occupied by local development servers
and infrastructure. Reserving one project-local default avoids that collision
without making the endpoint a protocol requirement or migrating an operator's
chosen configuration.
