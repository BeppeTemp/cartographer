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

**Status: implemented (2026-07-24); amended by [D134](#d134)** — the remote-less
creation this entry introduced as the default is now an explicit `--no-remote`
opt-out.

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

**Status: implemented (2026-07-24); partially superseded by [D121](#d121)** —
for native local upgrades the hint now names `cartographer upgrade-repair`, and
the "never restart from a cask hook" reasoning below no longer holds. Skew
reporting for remote servers and Kubernetes is unchanged.

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

---

<a id="d121"></a>
## D121 — Native local upgrades repair themselves

**Status: implemented (2026-07-31).** Supersedes the manual-restart portion of
[D95](#d95), for supported native local package upgrades only.

**Decision.** `cartographer upgrade-repair` is a non-interactive, idempotent
command invoked by both official native update paths — `install.sh` and the
Homebrew Cask post-install hook generated from `.goreleaser.yaml` — and
available for manual retry. On a **running** installed service it gracefully
replaces the process (`launchctl kill SIGTERM` relying on the plist's
`KeepAlive`; `systemctl --user restart` on Linux) and polls `/health` until it
returns `200`, `status:"ok"` and the installed binary version, then reconciles
the already-configured providers through the same in-process sync runner as
plain `cartographer sync`. `cartographer service restart --wait` exposes the
same version-gated replacement on its own; plain `restart` is unchanged.

**Safety boundary.** The effective config is discovered from the installed
plist/unit (the argument after `serve --config`), so a custom-config
installation is never verified against the standard endpoint; an installed but
malformed definition fails **before** any process is signaled. Replacement is
`SIGTERM`, never `kickstart -k`, so in-flight HTTP requests drain and pending
pushes flush. Proof is bounded: connection failures, non-200 responses and the
previous version are transient and retried, a malformed `/health` body fails
fast, and the timeout error names the endpoint, the expected version and the
last observed status/version. Zero mounted KBs are a valid state (`/health` is
`200` while `/ready` is `503`, D84), so verification uses health, not
readiness. A service that is stopped or not installed is left exactly as it is.
If `/health` already advertises the installed version the restart is skipped
entirely, which makes a retry after a partial repair free of a second drain.
Provider sync is best-effort and runs only when the client's `server_url` is
loopback HTTP on the same port as the native service — never `--auto-trust`,
never an invented approval, never `disconnect`/`connect`, never a deletion of
user-owned configuration. Exit codes separate the two failure domains: `1` is a
verified service with a pending sync (the binary update stands), `2` is a
running service that could not be verified (no sync attempted).

**Rationale.** D95 assumed an operator-initiated drain was the only safe way to
replace a running process, and forbade restarting from a Cask hook. That
protection is now provided by the sequence itself — graceful termination,
bounded health/version proof, preserved service intent — so the cost it
imposed (a skew warning the user had to act on, plus providers left pointing at
stale configuration until a manual sync) is no longer justified. D95's
version-skew reporting remains as-is for Kubernetes, remote servers, and for
any failure of this repair path.

---

<a id="d134"></a>
## D134 — `kb create` requires a git remote

**Status: implemented (2026-08-26); amends [D85](#d85).**

**Context.** A KB is a git repository, and its `origin` is what makes it durable
and reconstructible: the `kb-create` skill opens with a data-loss warning for
exactly that reason, and every sync path degrades silently without one
(`hasRemote` in `internal/kb/gitsync.go:27`, the `no_remote` git status, and
`CARTOGRAPHER_GIT_SYNC` documented as inert for a remote-less KB). The local
onboarding path did not enforce it: `cmdKBCreate` called `kb.Init` and nothing
else, and `docs/agent-install.md` presented the remote as optional, falling back
to a plain `kb create` whenever the user had not supplied one. Observed in the
field: a from-scratch installation completed successfully and left a local-only
KB, never prompting for a repository and never surfacing that the KB was neither
backed up nor syncable.

**Decision.** `cartographer kb create <name>` requires an explicit remote
decision: `--remote <url>` or `--no-remote`, with neither (or both) a usage
error (exit 2) that lists the three ways to get a KB — `--remote` for an empty
repository, `kb clone` for a repository that already holds a KB, `--no-remote`
for a local-only one. `--remote` attaches the URL as `origin` and pushes the
initial commit with upstream tracking (`gitx.PushSetUpstream`, added next to
`Push`, which must keep leaving tracking configuration alone for the sync
paths). A failure to attach or push removes the scaffold entirely and exits 1,
with hints separating an authentication failure from a non-empty remote — the
same cleanup guarantee `kb clone` already gives, so auto-discovery never mounts
a half-provisioned KB. `--no-remote` keeps the previous behavior and prints a
stderr warning naming the cost and the recovery commands. The agent runbook
(`docs/agent-install.md`) instructs the agent to ask for the repository before
creating anything and to fall back to `--no-remote` only on the user's explicit
acceptance.

**Rationale.** D85 correctly separated the local CLI path from the GitOps skill,
but made the remote optional to keep the path short, which made a non-durable KB
the outcome of the shortest sequence of commands a first-time user runs. The
product has no other moment where that state becomes visible: nothing prompts
later, and the sync machinery treats it as normal. Requiring the choice at the
only point where it is cheap to fix costs one flag and removes the silent
failure mode; keeping `--no-remote` preserves the throwaway/dev case without
making it the default.

**Compatibility.** Breaking for scripted `cartographer kb create <name>`
invocations: adding `--no-remote` restores the previous behavior exactly.

## D156 — Service `PATH`, `restart` after `stop`, and a `kb create` that keeps its scaffold

**Status: implemented (2026-08-28).** Amends [D73](#d73) (native local service) and
[D134](#d134) (`kb create` requires a remote). Closes #177.

**Context.** Three defects on the commands an operator runs on day one, each turning a
correct installation into something that reads as broken. From a field report on a large
migration.

1. `RenderLaunchdPlist` and `RenderSystemdUnit` set no environment at all, so the server
   inherited launchd's minimal `PATH` (or systemd's equally short one). A
   Homebrew-installed `sops` sits outside both, so every secret resolution failed with
   `secret_resolve: sops binary not found in PATH` — accurate and uninformative, from the
   definition `service install` itself had generated.
2. `Stop` used `launchctl bootout`, which **unregisters** the job, while `Restart` used
   `launchctl kickstart -k`, which requires a registered one. So `service stop` followed by
   `service restart` failed with `Could not find service … in domain` and only `start`
   worked.
3. `kb create` committed as the product default `cartographer@localhost` — `kb.Init`
   hardcoded it — which any forge with an author-membership push rule rejects; and on push
   failure `cmdKBCreate` deleted the whole scaffold. The outcome was no KB at all and a
   manual redo (`--no-remote`, amend the author, add the remote, push).

**Decision.**

- One `servicePATH(binPath)` feeds both definitions, so they cannot drift: the binary's own
  directory first (whatever installed Cartographer is the likeliest place to hold its
  companions), then `/opt/homebrew/bin`, `/usr/local/bin`, `/opt/local/bin`, then the
  platform default. **Curated and fixed rather than copied from the installing user's
  shell**, which would bake that user's whole environment into a service definition.
- `stop` keeps the job registered: on darwin it `disable`s it — necessary because the plist
  sets `KeepAlive`, which would otherwise restart the process immediately — and sends
  `SIGTERM`. `start` re-enables. `restart` falls back to `start` when the job is not
  registered, so a job booted out by an older version's `stop` stays restartable across the
  upgrade. `uninstall` keeps `bootout`: removing the definition is what uninstalling means.
  Registration is probed with `launchctl print`'s exit status, never by parsing its
  localized prose.
- `kb create` resolves the author in the same order the server does (service config, then
  git's own identity, then the product default) via a new `kb.InitWithIdentity`; `kb.Init`
  keeps its signature and its previous behaviour. It warns *before* pushing when it falls
  back to the default, which is when the operator can still act.
- **On push failure the scaffold is kept.** The local work is valid; only the push failed.
  The command prints the `commit --amend --author` and `push -u origin` lines and the
  `rm -rf` alternative, naming the data dir a server would otherwise auto-mount. The
  deleted-scaffold guarantee D134 inherited from `kb clone` protected against a
  half-provisioned directory being discovered; a *complete* scaffold does not need that
  protection, and paying for it with "no KB at all" was the worse trade.

**Consequences.** An already-installed service keeps its old definition: the `PATH` fix
needs a `service install` re-run, stated in `docs/deployment.md`. `TestRestart_Darwin_UsesKickstartK`
now expects `print` → `enable` → `kickstart -k`, and
`TestCmdKBCreateRemoteFailureCleansScaffold` became
`TestCmdKBCreateRemoteFailureKeepsScaffold` — both were asserting the old behaviour, which
is the behaviour this entry reverses. The initial commit message also moved from Italian
(`init: KB inizializzata`) to English, the only such string on that path.

---

<a id="d176"></a>
## D176 — The multi-KB readiness path gates on the audit sink too

**Decision.** `MultiKBServer`'s `/health` and `/ready` fold every mounted KB's
audit state into their verdict, name the degraded KBs, and expose each KB's
audit state under `kbs[].audit`. The rule itself lives in one place,
`auditGate`, shared with the single-KB handlers.

**Rationale.**

- **The audit-aware readiness code was unreachable on the HTTP path.** D119 made
  readiness gate on a required-mode audit sink so an operator, or a
  `readinessProbe`, notices before the next required-mode call is rejected. Only
  `Server.handleHealth`/`handleReady` implemented it. `MultiKBServer.Handler`
  answered `ready` from the mount count alone — and `serveHTTP` builds a
  `MultiKBServer` unconditionally, with the KB count only deciding the
  advertised server name. Every HTTP deployment, single-KB ones included, was
  therefore served by the handler that ignored the gate: a server that would
  refuse every write reported itself ready and kept receiving traffic.
- **One degraded KB makes the process not ready.** A probe must return one
  answer; the conservative one is the only safe choice, and a partially usable
  process is not a state a `readinessProbe` can express.
- **The degraded KBs are named.** Collapsing several KBs into one boolean tells
  an operator that something is wrong and nothing about where to look, which is
  the failure mode this endpoint exists to prevent.
- **`/health` stays liveness.** It keeps answering 200 with `status:"ok"` even
  when not ready: `auth.isPublicPath` exempts it, probes depend on that shape,
  and a liveness probe must never restart a process over a sink problem.
- **One shared fold.** Two implementations of one readiness rule is exactly how
  these two drifted apart; `auditGate` is small enough that sharing it costs
  nothing and asserting on it is cheap.

**Consequences.** A deployment whose required-mode audit sink is unhealthy starts
failing its readiness probe instead of silently rejecting writes — the intent,
and a change worth calling out in the release notes. Deployments with no sink, or
one in `best_effort` mode, are unaffected.

## D174 — `service status` reports observable state, not a verdict on nothing

**Decision.** `cartographer service status` keeps inspecting the **local native
service only**, and says what it observed rather than rendering everything as a
pair of booleans. `service.Status` gains `Lifecycle`
(`not_installed`/`not_loaded`/`loaded`), `HealthChecked` and `HealthSkipReason`;
the health line is printed only for a service that exists; a client pointed at a
different server is one line of context. `doctor` names the one combination that
cannot work.

**Context.** The starting report was a contradiction: a server answering `/health`
while `service status` said `healthy: false`. There is no contradiction — the two
speak about different things and both answer correctly, `/health` about the remote
server in `server_url` and `service status` about the local launchd/systemd job.
What was genuinely wrong is narrower, and all of it is a presentation defect.

- **No health verdict on what does not exist.** The `healthy` line was printed
  unconditionally, so a machine with no local service showed `healthy: false
  (http )`. Read quickly that is an outage; it is an absence. With
  `installed: false` the output now says there is no local service and how to
  install one.
- **`Healthy` conflated "unhealthy" with "not checked".** One boolean carried
  both meanings whenever the config was missing, unreadable, or configured for
  stdio. The probe now reports whether it ran, and why not — the root of the
  misreading was the missing distinction, not the probe.
- **An empty `http:` is stdio, not "apply the default".** `serve` selects the
  stdio path exactly when `cfg.HTTP` is empty. Substituting
  `defaults.DefaultListenAddress` in `Status` would report a health check against
  an address nothing listens on: a fabricated failure.
- **`Running` promised more than the probe delivers.** `launchctl print`
  succeeding proves the job is registered with launchd, not that a process is
  alive. The JSON field keeps its name (it is a contract) and its printed label
  became `loaded`, with `lifecycle` as the field to read.
- **`service status` is not widened to the remote server.** Answering about both
  would make it ambiguous; the remedy for the misreading is to *say* which one it
  inspects. A local service alongside a client pointed at a shared remote server
  is legitimate — context, never a warning.
- **One `doctor` finding, for the case that is actually broken.** `server_url` is
  loopback, nothing answers, and no local service is installed: the client points
  at a server that does not exist. The generic remedy, `service status`, would
  only repeat `installed: false`. The mirror case — a local service installed
  while the client points elsewhere — is deliberately not a finding.

**Consequences.** Output and JSON fields only. Existing fields and the
systemctl-like exit codes (`0`/`3`/`4`) keep their meaning, so `install.sh update`
is unaffected. A script reading `healthy` should read `health_checked` next to it;
one that wants the state should read `lifecycle`.

## D173 — `kb` commands: bounded clone, correct local target, read-only list

**Decision.** `gitx.Clone` takes a context and a non-interactive environment;
`kb create`/`kb clone` accept `--config` and refuse to act on the local data dir
when this machine's client points at a remote server (`--local` opts out); `kb
list` reports what is on disk and what the server serves, writing nothing.

**Context.** Three defects in the same family: a command that can hang with no
output, one that silently acts on the wrong directory, and a state nothing on the
CLI could observe.

- **Bounded execution over a diagnosis.** `Clone` was `exec.Command` +
  `CombinedOutput()`: no deadline, no progress, nothing stopping git from opening
  a credential or host-key prompt against a stdin that cannot answer. A hang was
  observed; which prompt fired is not asserted, because the remedy is the same
  either way — `GIT_TERMINAL_PROMPT=0`, `BatchMode=yes -o ConnectTimeout=10` for
  ssh remotes, `--timeout` (default 120s), and streamed progress.
- **Never force host-key acceptance.** `StrictHostKeyChecking=accept-new` would
  trade a hang for a silent trust-on-first-use decision on an operator's machine.
  Git fails quickly and says why; the operator accepts the key themselves.
- **An operator's `GIT_SSH_COMMAND` wins.** Someone who configured a proxy
  command, an identity file or a jump host has said how to reach their forge.
  Overwriting that breaks a working setup to prevent a hypothetical one, so the
  default is added only when neither the process environment nor the caller
  provides one.
- **Cleanup removes only what the command created.** The destination is checked
  not to exist before the clone, so anything under it afterwards is ours. An
  interrupt now cancels the context — killing git and waiting for it to exit —
  before removing the tree: deleting a directory a running git is still writing
  produces a second, more confusing failure, and the previous `defer` never ran
  on SIGINT at all.
- **The wrong target is an error, not a warning.** `resolveServerDataDir` read
  the standard service config path while `service install --config` accepts
  another, so a service installed at a custom path was invisible and the command
  reported `KB "x" mounted at …` about a directory nothing reads. `--config`
  fixes the resolution; the guard covers the other half — a client pointed at a
  remote server means these commands would act on a server nobody is talking to.
  A command that claims to have mounted something it did not is the worst outcome
  available, so it fails, with `--local` as the declared opt-out and no opinion at
  all when `--data` already names the target or no client config exists.
- **`kb list` is strictly read-only.** It must not call `kb.Open`, which
  self-migrates the local git-exclude entry of every repository it touches: a
  listing command that mutates what it lists is not one. Validity is read
  directly from `data/index.md`, which is what `Open` itself checks. A missing
  data dir is reported, never created — that is `serve`'s job, not a listing's.
- **"Not mounted" and "could not ask" are different answers.** When `/health` does
  not respond the `MOUNTED` column disappears and the reason is printed. Rendering
  an unreachable server as "nothing is mounted" would invent a fact.

**Consequences.** A new subcommand, new flags (`--config`, `--local`,
`--timeout`), and a guard that starts **failing** a usage which is silently
ineffective today — worth naming in the release notes together with `--local`.
`kb create --remote` stays mandatory (D134); this changes nothing about it.

## D177 — `kb rename` is offline, bounded, and says what it does not migrate

**Decision.** `cartographer kb rename <old> <new>` renames a KB's mount point —
the directory and the matching `kbs[]` entry, together or neither — after a
preflight that reports every other reference to the name it can find. It never
contacts a server, a client or a git remote.

**Context.** Renaming a KB meant moving the directory by hand and editing
`server.yaml`, with nothing checking that the two stayed consistent and no way to
see what else depended on the old name. A KB's name is an identity: the HTTP
endpoint (`/mcp?kb=`, `/mcp/<name>`), the derived tool prefix, auth scopes
`kb:<name>:r|rw`, and the client-side `signing_keys` / `mcp_approvals` keys.

- **Bounded scope, honest rollback.** Directory plus server config, both local,
  is the only pair this command can undo. A command that promises to fix
  everything is one that will half-fix something, so everything else is reported
  instead: scopes, role rules and client-side pins are configured out of band and
  are not rewritten here or by a later sync. Silently editing an operator's auth
  configuration would be the worse failure.
- **Offline by construction.** The git `origin` is untouched — renaming a local
  mount point is not renaming a repository — and clients need no orchestration:
  `removeMCPEntries` + `applyMCPEntries` already perform the rename on the next
  sync, so driving them from here would duplicate a mechanism that works.
- **The directory moves first.** It is the step that can fail for reasons outside
  the process (permissions, a different filesystem). A failed config write renames
  it back; that is the only rollback claimed, and it is implemented.
- **A cross-device rename fails.** Falling back to a recursive copy would silently
  change the ownership and timestamps of a git repository. The operator is told to
  move it themselves.
- **Ambiguity refuses, absence does not.** Two `kbs[]` entries that could both be
  the KB is a refusal naming them, because guessing detaches a KB from its
  configuration. **No** entry is not: a KB created by `kb create` or found by
  discovery legitimately has none (D151), and refusing there would make the
  command useless in its most common case — the directory rename is then the whole
  job, and the output says so.
- **A derived prefix is announced, not prevented.** With
  `mcp.tool_prefix_mode: kb-name` the rename renames every tool the agents see.
  That is a legitimate consequence of renaming a KB; the failure mode to avoid is
  discovering it afterwards, so both prefixes are printed first. An explicit
  `kbs[].tool_prefix` is preserved verbatim.
- **The preflight writes nothing**, including no `kb.Open` — which self-migrates a
  repository's git-exclude entry. Validity is read from `data/index.md` directly,
  the same file `Open` checks.
- **The config is edited as a YAML node tree**, so comments, key order and
  unmodelled fields survive: a rename must not reformat a hand-written server
  config.
- **No restart.** `--restart` is offered, mirroring `kb create`/`kb clone`, and a
  failed restart does not roll the rename back: the files are already consistent,
  and undoing them because a supervisor misbehaved would leave a worse state.

**Consequences.** A new subcommand; nothing existing changes behaviour. Minor.


## D192 — Onboarding and release hygiene: text that lied, hints that could not be run, paths that ended half-done

**Status: implemented.** Closes #246.

**Context.** A read-only audit of installation and onboarding found the machinery solid — one
binary for server and client, Homebrew plus a POSIX installer, four platform targets, an idempotent
`service install`, CI running vet/test/smoke/E2E/installer — and the **documentation and edge
paths** lagging behind it. Fifteen items, each verified against the code, none a design question.
They are one decision rather than fifteen because they share no invariant and none blocks another:
grouping them kept fifteen trivial PRs from competing with the substantive work.

**Decisions.**

- **Text that was false is corrected at the source, not paraphrased.** `bindingNotYetEnforcedNote`
  still told the user "bindings are recorded but not yet enforced during sync" — true under D169,
  false from D170 on, and pinned by a test asserting its presence. The constant and its three call
  sites are gone, and the test now asserts its **absence**, which is what stops it coming back.
  `docs/deployment.md` claimed `install.sh update` restarts a running service, contradicting the
  paragraph immediately above it describing `upgrade-repair` (D121). `docs/agent-install.md` said a
  failed `kb create --remote` scaffold "was already removed" while D156 deliberately keeps it and
  prints how to fix or remove it. The `cartographer-ops` bundled skill prescribed
  `brew upgrade` + `service restart`, when the Cask's own post-install hook runs `upgrade-repair`
  and no follow-up is needed. `kb-create` told the operator to hand-edit `.cartographer.yaml`,
  never mentioned `cartographer client bind`, and contradicted itself on KB naming.
- **Every hint the tools print can be pasted and run.** The no-KB message named only
  `kb create --remote <url>`, leaving an operator without a remote with no working form; it now
  names `--no-remote` too, with its cost stated. The installer's `PATH` warning now gives the two
  concrete next steps — invoke the printed path, or add it to `PATH` — instead of stating a fact
  and stopping.
- **`uninstall` refuses rather than leaving a half state.** It removed the binary only, so a
  machine with a service installed kept launchd/systemd units pointing at a missing executable. It
  now detects the units, names them and the commands that remove them, and exits non-zero — unless
  `--binary-only` says the operator meant exactly that, in which case it proceeds and states what
  it left behind. A coordinated teardown that deletes a user's KB data is not something an
  installer should do implicitly, so it still does not.
- **A checksum file that does not cover the asset is an error.** `install.sh` skipped verification
  when the entry was missing, which is the shape a truncated or tampered manifest has. A release
  that ships no `sha256sums.txt` at all remains installable — older tags have none, and refusing
  them would break a legitimate downgrade. Four scenarios now cover the matrix; the mismatch case
  asserts no binary is left behind.
- **The Cask deprecation is upstream's, and is recorded as such.** Item 13 of the plan prescribed
  replacing `postflight` with `postflight_steps` in `.goreleaser.yaml`. Verified and **corrected
  during implementation**: that file contains no `postflight`. It uses
  `homebrew_casks[].hooks.post.install`, GoReleaser's current API since v2.13; the deprecated
  stanza is what GoReleaser *emits*, confirmed by generating the Cask locally with GoReleaser
  2.18.1, which still writes `postflight do`. No setting here changes it. The finding is recorded
  as a comment next to the hooks block with the version and date, to be re-checked after a
  GoReleaser bump. Hand-writing a stanza the template does not own was rejected.
- **The two undocumented destinations get an alarm, not a move.** Codex skills materialize under
  `.codex/skills` while the vendor documents `$HOME/.agents/skills`; OpenCode agents under
  `.opencode/agent` while the vendor prefers `.opencode/agents`. Both work against the real
  clients. A destination change is a migration — prune the old files, re-key the lockfile — and for
  Codex the right target is the *repository* path, which only exists once a workspace scope does
  (D193), so moving it now would mean doing it twice. What was missing was the alarm: a test
  asserts the **declared** destination against the client's own discovery output, so it survives
  D193 changing that destination.
- **The compatibility test skips where it cannot answer, and only that.** It skips when the client
  is absent, and also when the client errors, times out or prints nothing — an unrelated client
  problem must not turn this into a red suite everyone learns to ignore. Only a successful run
  whose output does not mention the declared directory is a signal. On the machine that
  implemented this, `codex debug prompt-input` answered and `opencode agent list` did not, which is
  exactly the case the skip exists for.

**Invariants kept.** The installer stays POSIX `sh` and network-free under test. The GoReleaser
guard keeps asserting `upgrade-repair` through the stable linked binary. Bundled skills keep their
frontmatter contract. `uninstall` on a machine with no units behaves exactly as before.

**Consequences.** `install.sh uninstall` can now exit non-zero where it used to succeed —
behaviour change, release-note it. Everything else is documentation, corrected hints and test
coverage. The provider list in `docs/agent-install.md` (item 6) was already fixed by the
Antigravity work (D194) and needed no change here.
