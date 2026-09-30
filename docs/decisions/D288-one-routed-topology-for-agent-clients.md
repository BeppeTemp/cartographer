---
topic: transport-auth
---

# D288 — One routed topology for agent clients: the binding in the URL, tools never prefixed

**Decision.** Every HTTP server always serves `/mcp/routed`, and the client always writes one entry
per provider pointed at it. A provider's KB binding travels in the URL (`/mcp/routed?kbs=kb-a,kb-b`):
the server serves that connection a schema with a required `kb` enum for two or more KBs, and no `kb`
argument at all for exactly one. `mcp.mount_mode`, `mcp.tool_prefix_mode` and `kbs[].tool_prefix` are
deprecated for one release (accepted, ignored, one startup warning each, never fatal); tools are never
prefixed. The per-KB endpoints (`/mcp?kb=`, `/mcp/<name>`) stay as plumbing. Amends
[D187](D187-one-tool-surface-for-a-multi-kb-server-a-routed-mount.md) (the mount stops being opt-in);
deprecates D102, D152 and D153.

**Why.** D187 made routing opt-in only to leave existing deployments untouched; it never argued that
per-KB entries were the better topology, and they are not. Measured on a real five-KB server, `tools/list`
is about 33 KB (about 8.3k tokens) per mount, so a provider bound to all five pays about 41k tokens on
every round-trip against about 10k routed. Everything a per-KB entry gave an agent client is available on
the routed mount: a single-KB connection needs no `kb` (the URL names it, exactly like `/mcp/<name>`, so
this is not the inference D187 rejected), a flat-namespace client cannot collide with one entry
(D102/D152/D153 have nothing left to solve), and a provider sees only its bound KBs because the server
restricts `kb` to `?kbs=`. The one thing lost is client-side permission rules that match a tool *name* per
KB, since names now carry no KB; real authorization is per-KB scopes (D44/D45) and read auto-approval by
`readOnlyHint` (D76) is unaffected.

**Alternatives rejected.**
- Keep routed opt-in and improve per-KB: leaves the N-copies cost as the default for every multi-KB client.
- Infer the KB from the call or default to the first one: a slip writes into the wrong archive; `kb` stays
  explicit whenever the URL left more than one open.
- Let `?kbs=` widen access: the binding only narrows, per-KB authorization still runs at the target.
- Silently drop an unknown name in `?kbs=`: a typo would hide a KB without a trace, so it is a `400` naming
  the name and the mounted KBs.
- Fatal errors for the deprecated keys: an upgrade must not fail to start on a config that used to be valid.
- Remove the keys at once: one release of warnings first; the removal is a follow-up.
- Require `reconnect` to migrate: `sync` already removes every name the client may have owned and writes the
  live shape, so one `sync` heals the topology.

**Consequences.** Breaking for clients: entry and tool names change (`cartographer-kb-a` with `kb_a__search`
becomes `cartographer` with `search`), user-written permission rules naming the old tools must be updated,
entries are rewritten by the next `sync`. The server builds one routed `Server` per distinct effective KB set
and caches it (bounded by the subsets of the mounted KBs), because `tools/list` now differs per connection.
A KB named `routed` is skipped with a warning (it collides with the endpoint's path). `/health` always reports
`mount_mode: routed` and `routed_path` in HTTP mode when at least one KB is mounted and no longer a per-KB
`tool_prefix`; a client that finds no `routed_path` (an older server) keeps writing per-KB entries with the
existing Kiro and Antigravity warnings. With `auth: false` the binding is advisory (a client can edit its URL),
exactly as true of per-KB entries today; isolation between providers needs per-provider tokens with per-KB
scopes. Stdio is unaffected. A follow-up removes the deprecated keys and the prefix code (including the
`cartographer kb rename` prefix note and the `tool_prefix` capability row in `kb_status`).
