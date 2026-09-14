---
topic: deployment-release
---

# D85 — `kb create` and first-KB onboarding: a CLI command, not only the agentic skill

**Status: implemented (2026-07-24); amended by [D134](D134-kb-create-requires-a-git-remote.md)** — the remote-less
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
