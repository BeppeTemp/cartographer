---
topic: deployment-release
---

# D121 — Native local upgrades repair themselves

**Status: implemented (2026-07-31); Homebrew path amended by [D199](D199-the-cask-s-install-steps-are-postflight-steps-the-next.md).**
Supersedes the manual-restart portion of [D95](D95-upgrade-transparency-through-version-skew-hints.md), for supported native
local package upgrades only. The Cask no longer runs `upgrade-repair` (its
install steps are sandboxed): the next `cartographer sync` does the replacement.

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
