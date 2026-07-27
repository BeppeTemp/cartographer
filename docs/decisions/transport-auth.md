# Transport and authorization decisions

stdio/HTTP transport, routing, bearer-token authorization, audit design and tool prefixes. Current behavior: [`../transport-auth.md`](../transport-auth.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d2"></a>
## D2 — Hand-rolled MCP stdio transport (no SDK)
Newline-delimited JSON-RPC 2.0, pure stdlib. Choice: full control + zero dependencies for the local Core. Streamable HTTP (Phase 1) is evaluated separately (D16). An MCP SDK can be introduced if needed now that D1 is resolved.

---

<a id="d16"></a>
## D16 — HTTP transport: hand-rolled Streamable HTTP
`net/http` handlers: `POST /mcp` (JSON-RPC), `GET /health`, `/.well-known/oauth-protected-resource` (RFC 9728). CORS headers. No SSE streaming for now (each request = one complete response).

---

<a id="d17"></a>
## D17 — Multi-KB: routing via query parameter
`MultiKBServer` selects the KB via `?kb=<name>`. With a single KB the parameter is optional. With multiple KBs and no parameter → 400 error. Simpler than path-based routing.

---

<a id="d18"></a>
## D18 — Audit log: JSONL hash-chain with opt-in Ed25519 signature (compliance-grade)
Each entry: `prev_hash` + `hash` = sha256 of `Timestamp|Tool|Args|AgentID|Outcome|PrevHash`. Genesis: `prev_hash = "genesis"`. `Verify` checks the whole chain. Args truncated to 1024 chars. Opt-in Ed25519 signature: if `CARTOGRAPHER_AUDIT_KEY` is set, each entry is signed (`sig` = hex of the signature of `hash`); `Verify` also verifies the signature if the key is available. Entries without `sig` (pre-signature logs) remain valid. `VerifyFull` distinguishes signed, unsigned, and invalid-signature entries.

**Wiring in `main.go`** (added): `CARTOGRAPHER_AUDIT_LOG` enables opening the log (`audit.Open`, or `audit.OpenWithKey` if `CARTOGRAPHER_AUDIT_KEY` is also set). The `*audit.Log` is passed to `serveHTTP`/`serveStdio`; wiring into the individual tool handlers is a future step.

---

<a id="d44"></a>
## D44 — Structured tokens with per-KB scopes + per-KB identity/SOPS fields

**Decision.** `config.TokenSpec{Token, Scopes}` replaces `[]string` in `AuthConfig.Tokens`,
with a backward-compatible custom `UnmarshalYAML` (legacy scalar = admin, or mapping with scopes). Format
`token|scope1;scope2`, scopes `kb:<nome>:r|rw`; a token without scopes = admin. `KBSpec` gains
optional per-KB overrides (git identity, `sops_age_key_file`); new `SopsConfig{AgeKeyFile}` —
the zero-value of each override = fallback to the global.
**Rationale.** This milestone is *config plumbing* only: the runtime stays unchanged,
the r/rw enforcement arrives in D45. Separating config from enforcement keeps each milestone green and
committable; the token format remains backward compatible with existing deployments.
Details: `docs/transport-auth.md` §Per-KB authorization (r/rw scopes, HTTP enforcement).

---

<a id="d45"></a>
## D45 — Per-KB r/rw scope enforcement: scoped `TokenStore` + body-peek HTTP guard + fail-closed read-only classification

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

---

<a id="d102"></a>
## D102 — Opt-in per-KB MCP tool-name prefix

**Decision.** Every mounted KB registers the same 20 tool names by default; a new opt-in, default-off
per-KB prefix lets an operator disambiguate them: `kbs[].tool_prefix` (explicit) or the global
`mcp.tool_prefix_mode: kb-name`/`CARTOGRAPHER_MCP_TOOL_PREFIX_MODE=kb-name` (derives the prefix from
the KB's own name for any KB without an explicit `tool_prefix`) registers that KB's tools as
`<prefix>__<tool>` instead of `<tool>` (`Server.SetToolNamePrefix`, applied once, inside
`RegisterTool`). The raw value is sanitized (lowercased, `[^a-z0-9_]+`→`_`, collapsed,
leading/trailing `_` trimmed) and validated at startup — empty or digit-leading after sanitisation,
or a resulting `<prefix>__<tool>` over 48 characters for any tool the KB registers, is a fatal config
error naming the KB and the offending name (`internal/config.ResolveToolPrefix`,
`MountKBWithPrefix`). Read/write classification and the `agent`/`full` tools profile strip the
prefix before matching (`Server.StripToolPrefix`), so scoped tokens and the profile filter are
unaffected by prefixing. `serverInfo.name` becomes `cartographer:<kb>` once 2+ KBs are mounted
(`Server.SetDisplayName`); a single-KB deployment keeps the historical bare `cartographer`.
`cartographer connect`/`sync` warn on stderr whenever the `kiro` provider is configured against 2+
MCP entries, independent of whether the server has prefixes set (`kiroFlatNamespaceWarning`).

**Rationale.** Claude Code, Codex and OpenCode namespace MCP tools per server, so a second KB's tools
never collide with the first's under those clients (verified empirically, GitHub issue #62).
Kiro CLI has one flat tool namespace across every configured server: without a distinguishing
prefix, mounting a second KB there silently drops its tools rather than erroring. Making the fix
default-off keeps the byte-identical tool surface for every client already unaffected by the
problem; making it per-KB (rather than always-on for multi-KB servers) lets an operator prefix only
the KBs that need it, e.g. to keep short names on the "primary" KB.

**Consequences.** The prefix is applied at exactly one point (`RegisterTool`), so every
conditionally-registered tool (`artifact_write`, `skill_install`, `sync_*`) is covered without a
second injection site. The 48-char budget is checked against the actual registered names after
`setupFn` runs, not computed analytically beforehand, so it naturally accounts for every tool a KB
ends up registering (including config-gated ones). The client-side warning cannot inspect whether
the server already mitigated the issue (`GET /health` doesn't expose tool prefixes), so it fires on
the precondition alone (kiro + 2+ entries) — a false positive (server already prefixed) is a
one-line stderr note, not a wrong outcome.
