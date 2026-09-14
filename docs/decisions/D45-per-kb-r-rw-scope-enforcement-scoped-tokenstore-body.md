---
topic: transport-auth
---

# D45 — Per-KB r/rw scope enforcement: scoped `TokenStore` + body-peek HTTP guard + fail-closed read-only classification

**Decision.** Connects runtime enforcement to the D44 config plumbing. `auth.TokenStore` moves
to `map[string][]KBScope` (`NewScopedTokenStore`, backward compatible with `NewTokenStore`).
Per-tool enforcement lives in the HTTP guard (`mcpAccessGuard`), not in the Middleware: if the token has
scopes, the guard reads the JSON-RPC body (`io.LimitReader`, 2MB) and **always restores** it onto
`r.Body` (the downstream handler re-reads from scratch), determines `needWrite` from `ToolRequiresWrite(tool)`
(**fail-closed** on unparsable JSON or unknown tool) and checks `auth.HasAccess`. New
field `Tool.ReadOnly bool` marks tools that never mutate the KB; `ToolRequiresWrite` consults a
dedicated map (`readOnlyToolNames`), verified against the real registry by a golden test
(`TestReadOnlyToolsGolden`) to avoid silent divergences.
**Rationale.** Fail-closed on both axes (unknown tool → write; scope with no match → 403)
because it is security-sensitive code: better one 403 too many than a silent bypass. The
body restore is tested explicitly because without it every scope-authenticated call would
silently break.
Details: `docs/transport-auth.md` §Per-KB authorization, `docs/control-plane.md` §Read/write
boundary.
