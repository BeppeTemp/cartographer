---
topic: client-configurator
---

# D1658 — `cartographer call` drives the stdio bridge with one stateless request, and its skill is gated by a client-side predicate

**Decision.** `cartographer call` builds the same `mcpbridge.Bridge` as `cartographer mcp` and feeds it one `tools/call` (or `tools/list`) line carrying the request `_meta`, with no `initialize`; the pipe stays open until the response with the request's `id` arrives. The command writes no file anywhere and works with a read-only `$HOME`. Exit codes: 0 the tool succeeded, 1 the tool returned `isError`, 2 everything else. The bundled `cartographer-cli` skill reaches a provider only if `configurator.UsesCLI` says so; the filter `withoutCLISkill` runs on the verified manifest of each projection, and no built-in descriptor sets the flag.

**Why.** A harness with a shell and no MCP needs the same scope rules as the bridge (provider, working directory, read-only default, author header); reusing the bridge gives them without a second scoping model. Bundled skills reach every client's manifest, so the server leaves this one out unless `BuildOptions.IncludeCLISkill` is set (no built-in caller sets it): that also covers an older client that has no filter. The client-side filter on the verified manifest stays as a second line, where the prune and the status counts follow from the manifest.

**Alternatives rejected.** Reimplementing the routed client in the command: duplicates scope resolution and the fail-closed read-only check. Closing the pipe after the request: the bridge aborts in-flight calls after its EOF grace. A capability marker in the `sync_pull` request: more protocol for a skill nobody receives yet. Projecting the skill to a named provider now: no provider is known to need it yet; the client variants mechanism sets the flag.

**Consequences.** A future change to `call` must keep the no-write invariant (tested with a tree snapshot and a read-only home). The test that no built-in provider uses the CLI is intentional and changes only with the variants work. `skill_install` by name copies the skill into a KB on purpose: that is a KB author's explicit act. A new provider-specific bundled skill needs its own predicate, never a manifest-wide default.
