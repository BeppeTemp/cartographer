---
topic: sync-provisioning
---

# D2056 — OpenCode's MCP section is written in the shape its major reads

**Decision.** The OpenCode MCP section — Cartographer's own entry (`connect`/`sync`) and a KB's
`mcp` artifact — is written under `mcp.servers.<name>` on OpenCode 2.x, and under the V1 flat
`mcp.<name>` on 1.x. The installed major is read once per run (`opencode --version`); a version
that cannot be read takes the native shape, the newest one, as D719 does for the hook plugin. The
native entry carries no `enabled` (2.x is enabled by default and spells the inverse, `disabled`)
and sets `oauth: false` when it carries request headers. One helper (`mcpServerPaths`) owns the
JSON paths each provider reads; merge, removal, name listing and lookup all go through it, and a
write replaces the same-named entry whole and deletes it from every other accepted shape.

**Why.** On 2.3.0 (probed 2026-11-13, #2055): `mcp.servers` is what `opencode mcp add` writes and
what `opencode debug config` reports; the V1 flat map is still normalized on load; and when both
hold the same name the native one wins, in `debug config` and in `mcp list` alike. Writing only
the flat shape therefore left the client on a stale entry — an old URL or token — that `sync`
could not heal (its comparison saw the flat entry as already correct) and `disconnect` could not
remove. Reading only the flat shape made `doctor` count the literal key `servers` as a server and
report the managed entry missing.

**Alternatives rejected.** Keeping the V1 shape only: it still works on 2.x through the normalizer,
but a native entry written by the client itself silently shadows ours, which is exactly the defect.
Writing both shapes: 1.x reads the flat map, so it would see a server literally named `servers`,
and 2.x merges both with the native one winning anyway. Gating on 2.3 exactly: `mcp.servers` is
the canonical V2 shape from 2.0 (the migration guide), so the major is the boundary, and a
minor-gated rule would keep writing the deprecated shape on 2.0–2.2 with nothing to gain. Writing
no entry when the version is unreadable: a `sync` from a timer has no `opencode` on `PATH`, and
withholding the entry is the failure mode of #1715.

**Consequences.** A file an older Cartographer wrote converges on the next `connect` or `sync`:
the native entry replaces the flat one of the same name. Running a 1.x client reverses the
convergence, and running 2.x again restores it. A user's other servers survive both directions,
and a native entry the client wrote for another server is untouched. `oauth: false` keeps a
bearer-header server from starting an authorization flow the server cannot serve. Residual risk:
an unreadable version on a 1.x install writes the native shape, which 1.x ignores; the normal case
is the client on `PATH`, which is read correctly. D1172 (the file's location) is unchanged.
