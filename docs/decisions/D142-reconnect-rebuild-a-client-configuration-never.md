---
topic: client-configurator
---

# D142 — `reconnect`: rebuild a client configuration, never automatically

`cartographer reconnect [provider|all]` is a full `disconnect` followed by a full `connect` for the
selected providers, in one invocation. It **reuses `doDisconnect` and `doConnect`** rather than
being a third implementation of either: the two halves already encode every rule about what may be
removed and what must be written, and a parallel implementation would drift from them silently.

**Why a rebuild is needed at all.** `sync` does more than fetch artifacts — it re-enumerates the
mounted KBs, rewrites the MCP entries, re-ensures the bootstrap hook and materializes — and covers
almost every kind of drift. What it structurally cannot cover is what an **older Cartographer
version** left behind: pruning is managed-only, so a generated plugin whose filename changed, a
managed block whose marker spelling changed, or a hook registered outside the block is not in the
current `managed[]` and survives every sync. The code already carries scars from exactly this —
`instructionsBlockBeginPrefix` exists solely to recognize blocks written by older versions in
another language, and D99's repair removes Codex hook registrations left outside the managed block
so a hook does not fire twice. The connect half writes the current shape from nothing, which is what
removes them.

**It is a rebuild, not a reset.** Every setting is read from `.cartographer.yaml` and re-applied:
server URL and name, auth mode, token env, trust, pinned signing keys, MCP approvals, search roots
and paths. That falls out of two existing rules rather than new code — `disconnect` never deletes
the file (D64), and `connect` reads it back — but it is the load-bearing property: a user who has to
re-approve every MCP descriptor after an upgrade will simply stop running the command.

**It is never automatic.** No upgrade path invokes it. The reasoning is D121's — automatic repair
never invents an approval and never broadens trust — plus the observation that removing and
rewriting provider configuration is a bigger hammer than repairing it in place. `upgrade-repair`
stays the automatic path and keeps calling the ordinary sync.

**Partial-failure contract.** If the connect half fails after the disconnect half succeeded, the
command exits 2, names the providers now left without a configuration, and prints the exact
`cartographer connect` invocation — same settings — that finishes the job. Silence there would leave
an agent without its MCP endpoint and no way to know it; a dry run leaves nothing disconnected and
therefore says nothing.

**The lockfile records the server version.** Each provider's `Lock` gains `server_version`
(optional, `omitempty`), taken from the `/health` snapshot `sync` already fetches — no second
request. Empty means "unknown" (every lockfile written before this, and any sync that could not
reach the server) and never triggers a report: the first sync after an upgrade must not tell every
user something it cannot know. An unreachable server leaves the recorded value **unchanged** rather
than blanking it, and a `dev` version on either side is ignored, the same rule the advisory
client/server skew line already uses. On a real difference `sync` prints exactly one line — both
versions and the `reconnect` recommendation — once per invocation regardless of provider count, then
proceeds with the ordinary sync. It reports; it does not escalate. `status` shows the same fact
without running a sync. The version is recorded **per provider** because providers are synced
independently.
