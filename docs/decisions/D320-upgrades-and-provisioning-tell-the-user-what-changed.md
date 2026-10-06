---
topic: sync-provisioning
---

# D320 — Upgrades and client provisioning tell the user what changed

**Decision.** Where the server or the client applies a change correctly but
leaves the user to discover its effect, it now says so, without writing anything
it did not write before:

- the update check re-asks GitHub inside the 24 h TTL when the cached latest
  equals the running version (`Options.ForceRefreshOnVersion`, used by
  `update notice` and the server's own check), and the generated instructions
  block tells the agent to relay a `kb_status` `latest_version` once, with the
  upgrade command, for every client whose session hook does not fire;
- a call to a pre-D288 prefixed tool name fails with a message naming the bare
  tool, the KB when its prefix can be mapped back, and a session restart;
- `doctor` gains `legacy_steering_pattern`, which flags prefixed tool names in
  the operator's own steering text outside the managed block;
- `sync` warns when an agent's `providers.<client>.tools` cites an `@<server>`
  the client has no MCP entry for;
- `status` stops repeating Kiro's session-hook limit once `connect` has shown it
  (`session_hook_limit_acked` in the lockfile);
- `cartographer paths suggest` proposes a path for every unresolved key.

**Why.** After the 0.18 cycle (D288, D291, D300) every remaining friction point
was a silent success: a release hidden by a cache refreshed an hour earlier, a
stale session told only `tool not found`, a steering file sync never touches, a
subagent restricted to a server that is not there, a hint printed on every
`status`, a placeholder whose default was already on disk. Each fix is a
message or a read; none changes what is written into a client.

**Alternatives rejected.**
- *Shorten the TTL*: costs a GitHub request per session for everyone to catch
  the one window; forcing only the "nothing newer known yet" case costs a
  request only until a newer release is cached.
- *Register hidden shim tools for every `<prefix>__<tool>`* (the plan's first
  shape): a registered tool needs an auth-policy and read-only classification
  and would enter the `TestReadOnlyToolsGolden` registry; the unknown-tool path
  (D151) already exists for exactly "explain a name we do not serve", so the
  shim is a message there, and no prefixed name can ever be listed.
- *A fixed regex for the old prefix* (`…_kb__…`): the D102 prefix was any
  sanitised KB name or an explicit `tool_prefix`, so the doctor pattern matches
  the shape `<word>__<tool-shaped word>` instead, and the routed message names
  the KB only when a served KB's name derives the prefix.
- *Validate `providers:` keys at sync*: D291 copies verbatim and promises
  nothing; the `@server` check is a heuristic warning that never blocks.
- *Let `status` record the Kiro acknowledgement*: `status` reads; only
  `connect`/`reconnect`, which always show the hint, set it.

**Consequences.** The instructions block's content changes, so every client
rewrites it once on the next sync. `doctor` can now exit 1 on a steering file
the operator wrote; the finding names the replacement. The `@server` check
reads only the client's global MCP config, so a project-scope apply is not
checked. `paths suggest` writes nothing and its repo matching is a substring
heuristic: it proposes, the operator confirms with `paths set`.
