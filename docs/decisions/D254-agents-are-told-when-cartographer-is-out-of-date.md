---
topic: client-configurator
---

# D254 — Agents are told when Cartographer is out of date

**Decision.** The session-start bootstrap hook (D60) runs `cartographer update notice` after its
silent sync and keeps that command's stdout. It prints nothing unless the GitHub release list
names a newer tag, then one paragraph addressed to the agent: the versions, the upgrade command
for the channel the running binary came from, and the behaviour itself: tell the user once, offer
it, run it only on consent. The answer is cached for 24 h and a lookup that fails or takes more
than 3 s means "unknown". `status`, `doctor` and the dashboard read the same cache and never the
network. The server runs its own daily check and reports `latest_version`, versions only, in
`/health` and `kb_status`. Upgrading without the user is opt-in (`update.policy: auto-patch`) and
limited to patch releases through Homebrew, `install.sh` or `install.ps1`.

**Why.** Nothing knew a newer release existed: D95's skew compares client with server, so a
machine where both are one release behind reported `in_sync` forever. The hook already runs at
every session start of every hook-capable client, and Claude Code adds a SessionStart hook's
stdout to the context, so an agent can hear about it with no new trigger. Putting the behaviour in
the notice's text, not in KB instructions, makes it work for curated instructions that skip the
generated preamble, and needs no per-KB change. An upgrade replaces the running server and has
needed `reconnect` before, so the default is consent. The source is `/releases?per_page=1`, not
`/releases/latest`, because every 0.x tag is a pre-release (D82, D211). What it costs: one GitHub
request per machine per day, unauthenticated unless `GITHUB_TOKEN` is set, and a notice text that
must be kept short, because it lands in every session's context while an update is pending.

**Alternatives rejected.**
- Upgrading silently by default: an upgrade can require `reconnect` and restarts the service
  under open sessions, which is the user's call. A 0.x minor can also break things (D190/D191).
- One upgrade command for everyone: installing from a different channel leaves two binaries on
  `PATH`. The command is derived from the binary's path, and none is named when the channel is
  unknowable (container, unknown).
- The server naming a command: its deployment (image, Helm, a native service) is not knowable
  from inside, and a remote server's operator is not at the client.
- A per-KB instruction telling agents to check: it needs every KB changed and is skipped by
  curated instructions.
- Checking on every `status`/`doctor` run: it adds network latency to commands that must stay
  fast and work offline. They read the cache the hook refreshes.
- `/releases/latest`: it skips pre-releases, so it would 404 while no stable release exists, and
  later report the last stable release instead of the newest one.

**Consequences.** A session start stays silent unless an update exists. A warm cache means no
network call, and no failure is ever printed. `status` stays `cartographer.status/v1` with the
additive `update` and `server_latest_version` fields, and `/health` gains `latest_version` only
when a newer release is known. Neither changes an exit code. Already-connected machines get
the new script from their next `sync`, because `EnsureBootstrapHook` rewrites it unconditionally
(the hook is excluded from diffing, so nothing else would notice it changed). A test pins that
rewrite: making it conditional would strand existing machines until `reconnect`. `auto-patch`
never touches go-install, container or unknown channels, never takes a minor or major release,
and never retries a version whose apply failed: the notice falls back to the manual form and
names `update.log`. A `dev` build never checks.
