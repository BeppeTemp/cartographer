---
topic: sync-provisioning
---

# D737 — A workspace binding scopes the tools through a local stdio bridge

**Decision.** A provider with at least one workspace binding gets, as its global MCP entry,
`cartographer mcp --provider <p>` (a stdio command) instead of the HTTP URL. The bridge resolves its
working directory (`os.Getwd`, never `$PWD`) with the binding table (`clientconfig.ResolveWorkspace`,
deepest binding wins) and proxies the session to the routed endpoint with `?kbs=<set>`, which the
server already enforces. A folder under no binding keeps the provider's default set; an empty set
(`--no-kb`, or every bound KB unmounted) is answered locally as a session with no tools and never
sent as an empty `kbs=`; a client that declares `roots` while the folder is unbound is scoped by the
union of the bindings its roots resolve to. A provider with no binding keeps its HTTP entry.
Supersedes [D717](D717-workspace-scoped-server-entry.md).

**Why.** D717 scoped the tools by writing a same-named project entry, which works only where a
project entry replaces the global one (Claude Code, from vendor docs), writes `.mcp.json` and its
siblings into the user's repositories, and needs a probe per client and release to stay true. What
every client does provide is the spawn directory of a stdio server (probe 2026-10-17: Claude Code,
Codex, Kiro CLI, OpenCode, Crush, Copilot CLI; OpenCode with `$PWD` set to `$HOME`). It costs a
process per session and `cartographer` on `PATH`.

**Alternatives rejected.** Flipping `ProjectMCPShadowsGlobal` per client after probes: still writes
into repositories and depends on each client's merge rules. Server-side `roots` over HTTP: the server
knows neither the machine's paths nor its bindings, and half the clients do not declare `roots`. A
`${VAR}` in the entry URL set by direnv: per-folder configuration and env expansion in every client.
Session-start hooks: they see the cwd but cannot restrict a tool call. A per-workspace token: the
hard boundary, left out of scope (D44/D45).

**Consequences.** `?kbs=` is a scope filter, not a security boundary: the token in the session's
environment reaches every KB it allows. Nothing is written into a workspace for the tool scope;
`PruneServerEntry` removes what D717 left, on every project projection. The scope is fixed at the
start of the session (roots are asked once, after `initialized`). The entry command is the one found
on `PATH`, never `os.Executable()` (a versioned Cellar path); Codex needs the token variable in
`env_vars`. Release impact: a provider with a binding changes its global entry from HTTP to a stdio
command on the first `sync` after the upgrade. Antigravity and Kiro IDE were not probed; a host that
spawns outside the workspace and declares no roots gets the default set.

Limits. When the folder is unbound and the client declares `roots`, a server cannot ask for them
before it has answered `initialize`, so the bridge answers that handshake itself with instructions
that name no KB, then asks once after `initialized`; the tool list that follows carries the real set.
The bridge does not open the optional GET stream, so server messages outside a POST response are
lost (the routed server sends none); it sends a DELETE for a server-opened session on exit. Where
`cartographer` is not on `PATH` at `sync` time (a hook with a minimal `PATH`), the provider keeps its
HTTP entry and `sync` warns: its bindings do not narrow the tools until the next `sync` finds it.
