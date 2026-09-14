---
topic: transport-auth
---

# D152 — Tool-prefix uniqueness is enforced, and the client warning reads the prefixes

**Status: implemented (2026-08-28).** Amends [D102](D102-opt-in-per-kb-mcp-tool-name-prefix.md), [D120](D120-tool-prefix-discovery-for-client-owned-multi-kb.md) and [D144](D144-the-d102-tool-prefix-mitigation-reaches-the-agent.md).
Closes #173.

**Context.** Two independent defects, both cheap, and one of them meant a deployment that had done
everything right could still be silently ambiguous.

**"Prefixed" did not imply "unambiguous".** `ResolveToolPrefix` resolved one KB's prefix in
isolation and there was **no uniqueness check anywhere in the resolution path**. Two mechanisms
made collisions reachable: an explicit `kbs[].tool_prefix` was never compared against the other
KBs', and the derived form is **lossy by design** — `SanitizeToolPrefix` lowercases and collapses
every run outside `[a-z0-9_]`, so `my-kb`, `my_kb` and `My KB` all yield `my_kb`; its own doc
comment already showed two inputs mapping to one output. `ValidateToolPrefixShape` only checked
non-empty and not-digit-initial. On a flat-namespace client the result is a question about KB A
answered from KB B, with no error at call time and nothing in the logs.

**Two warnings implemented one check, and the wrong one was the loud one.** The server-side
`flatNamespaceMountWarning` fires only when **two or more** KBs actually registered unprefixed
tools, names them, and offers both remedies — it works from what the server really mounted, so it
is authoritative. The client-side `kiroFlatNamespaceWarning` fired whenever a flat-namespace
client had **two or more MCP entries**, regardless of prefixes, named no KB and mentioned only the
per-KB flag. On a deployment with three KBs and one of them unprefixed the situation was *safe* — a
single unprefixed KB cannot collide with anything — the authoritative warning was correctly silent,
and the client one fired on every sync. It is the one an operator reads, and it sent a maintenance
session after a problem that did not exist.

The source documented the trade-off deliberately: *"enumerateKBs returns names only, so the client
cannot tell whether the server mounted those KBs with prefixes; making this warning prefix-aware
would mean plumbing GET /health's KBInfo.ToolPrefix through it to avoid one stderr line of false
positive."* The reasoning was sound and **the premise no longer held**: `/health` has advertised
each KB's effective prefix since D120.

**Decision.**

- **Uniqueness is validated on the sanitised result**, since that is what creates the collisions,
  and a duplicate is **fatal at startup** rather than a warning: it is a configuration error with
  no safe interpretation, and `serve.go` already fails fast one branch away for a shape error. The
  message names both KBs, the colliding prefix, and the raw input when it differs. The check runs
  before the KB is appended, so a fatal leaves nothing half-mounted. The predicate lives in
  `internal/config` so it is unit-testable without a server.
- **Unprefixed KBs are exempt.** An empty prefix is the absence of one; two unprefixed KBs are the
  case `flatNamespaceMountWarning` covers deliberately as a warning, because a
  per-server-namespaced deployment must keep working untouched (D102). Turning that into a failure
  is D153's decision, not this one's.
- **The client warning adopts the server's predicate** (`>= 2` unprefixed), evaluated on `/health`
  data the caller already fetched — `effectiveToolPrefixes` is pure, so there is no extra round
  trip. It names the colliding KBs, as the server-side message does, and adds
  `mcp.tool_prefix_mode: kb-name` to its remedies: mentioning only the per-KB flag is what led the
  reporting deployment to add per-KB flags by hand instead of one global line.
- **The client-side copy is kept rather than deleted.** It is the only warning printed at
  `connect`/`sync` time, which is when the operator is actually looking.
- **When `/health` cannot be read, the warning stays unconditional** and says why. A missing signal
  must not silently become "all good" — the same rule the sync-timer check follows.

**Consequences.** A previously-starting server can now fail to start, but only when two mounted KBs
resolve to the same prefix — which was already broken, silently. `mcpEntry` gained a `KBName` field
so an entry can be paired with the KB facts `/health` reports for it. The client warning becomes
quieter on correct deployments, which is the point.
