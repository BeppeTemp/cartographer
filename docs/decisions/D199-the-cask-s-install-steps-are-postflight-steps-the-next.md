---
topic: deployment-release
---

# D199 — The Cask's install steps are `postflight_steps`; the next sync repairs the service

**Status: implemented (2026-09-12).** Amends [D121](D121-native-local-upgrades-repair-themselves.md) for the Homebrew path; supersedes
[D192](D192-onboarding-and-release-hygiene-text-that-lied-hints.md)'s finding that the Cask deprecation was not fixable here.

**Context.** Every `brew install`/`upgrade` printed `Calling postflight is deprecated! Use
postflight_steps instead`. It is an `odeprecated`, which Homebrew's normal cycle turns into an
`odisabled` — at which point the Cask no longer loads and every upgrade fails. D192 recorded it as
upstream's: GoReleaser (2.18.1, and its `main` at the time of writing) still renders
`homebrew_casks[].hooks.post.install` as `postflight do`. That is true of `hooks`, but
`custom_block` is GoReleaser's own escape hatch for stanzas its template does not model, and it
renders verbatim at the top of the Cask.

The replacement is not a rename. `postflight_steps` is a declarative DSL (`run`, `move`,
`symlink`, …) executed in Homebrew's sandbox: `HOME` points at a temporary directory, reads of the
real home are denied and network access is off unless a step asks for it. `upgrade-repair` needs
all three — the service definition in `~/Library/LaunchAgents`, the client's
`.cartographer.yaml` and every provider's configuration, `launchctl`, and `/health` on loopback.
Punching those holes (`network_access`, the real `HOME`, a `writable_paths` entry per provider
directory) was rejected: the list would have to track every provider added, and `launchctl` inside
the sandbox is unverified.

**Decision.**
- The Cask declares only the quarantine removal, as `postflight_steps` written through
  `custom_block` (`run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine",
  "{{staged_path}}"]`), and sets no `hooks`. Verified by installing the generated Cask from a local
  tap: no deprecation warning, the step runs inside the sandbox.
- The repair moves into the binary: `cartographer sync` — the session-start hook, the scheduled
  timer or a manual run — first replaces a native service still running the previous binary, with
  the same `Manager.Replace` as `upgrade-repair` (graceful `SIGTERM`, bounded `/health` + version
  proof), then syncs as usual. It acts only on unambiguous evidence: an installed, **running**
  service whose program resolves to **this same file** (so a service running another binary — an
  `install.sh` copy next to the Cask, a dev build — is never restarted in a loop that cannot
  converge), reached over the loopback endpoint this client syncs against (D121's eligibility
  rule), answering `/health` `ok` with a **different, non-empty** version. An unreachable or
  unhealthy service, a `dev` build, a dry run: nothing happens and sync's own diagnostics apply. A
  failed replacement is a warning naming `cartographer upgrade-repair`; the sync proceeds.
- It runs under the client-state lock (D172), so concurrent session-start syncs replace the
  service once: the next one in line finds it current.
- `install.sh update` and `upgrade-repair` are unchanged: outside a sandbox the eager repair still
  works, and the lazy one then finds nothing to do.

**Consequences.** After `brew upgrade` the old process keeps serving until the next agent session
start (or timer tick, or `cartographer sync`), which then pays a few seconds of drain and
relaunch once. `cartographer status` keeps reporting the skew in between. The
`test-install` guard asserts the new contract on the template's YAML (comments excluded):
`postflight_steps`, the quarantine removal on the staged path, and no `hooks`, `postflight do`
or `upgrade-repair`.
