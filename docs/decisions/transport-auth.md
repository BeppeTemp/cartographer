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

## D118 — Fine-grained RBAC and permission-aware retrieval

**Decision.** Authorization moves from a per-KB read/write scope to a per-principal *policy*
evaluated at a single point in dispatch. `auth.roles` declares named allow rules
(`kb` + `access: r|rw` + optional `maps`/`journals`/`types` selectors, `internal/config.RoleSpec`,
validated by `ValidateAuthRoles`); a token references roles by name and may also carry a stable
`id`. Roles compile into immutable `auth.Permission`/`auth.Policy` values
(`cmd/cartographer.scopedTokensWithRoles`) that are unioned with any legacy scopes, and the
middleware puts one `auth.Principal` in the request context. `internal/mcpserver/policy.go` resolves
every call against that policy through `resourceClassForTool`, an exhaustive registry inventory:
exact-concept, collection, source/destination, or whole-KB. Retrieval enforces the same predicate
inside the index rather than after it — `SearchFiltered`, `SearchFTSFiltered` and
`AllEmbeddingsFiltered` apply it **before** the limit. Writes are re-authorized under the git lock
via `reauthorizeUnderLock` immediately before mutating.

**Rationale.** A KB is the wrong authorization unit for a shared wiki: an agent that must write
runbooks under `infra/` should not thereby be able to rewrite every other map, and the read side is
where a leak actually happens — search, listings and semantic neighbors surface content the caller
was never meant to enumerate. Filtering *after* the limit would have been much simpler, but it turns
pagination into an oracle: a short page tells the caller exactly how many hidden concepts matched.
Selectors are intersected and there are no deny rules so that policy evaluation stays deterministic
and order-independent; adding a role can only widen access, never silently narrow another one.
Re-checking under the lock closes the window in which a concept's type changes between the dispatch
decision and the commit. `resourceClassForTool` is exhaustive rather than defaulting, so a tool
added without a deliberate choice is denied instead of inheriting whole-KB semantics.

**Consequences.** Forbidden and missing exact resources are deliberately indistinguishable: both
return `genericNotFound`, which means an operator debugging a 404 cannot tell from the response
whether the concept exists — the answer is in the role config, not in the API. FTS pagination reads
ranked rows in bounded batches (`ftsSearchBatch`) and stops at the requested limit, so a heavily
filtered query costs more reads than an unfiltered one. `AllEmbeddingsFiltered` applies the
predicate per candidate vector, so semantic search on a narrow role pays a per-concept frontmatter
read; the trade was accepted over caching a per-principal view, which would have to be invalidated
on every write. `auth.roles` is YAML-only — a rule is a structured object and the
`CARTOGRAPHER_TOKENS` string form cannot express it — so a deployment configured purely through
environment variables keeps whole-KB granularity. Principal IDs are derived from a token digest when
not set explicitly, replacing an earlier warning path that logged an 8-character plaintext token
prefix. Legacy behavior is preserved end to end: a token with neither scopes nor roles is still an
admin, and `Policy.Admin` bypasses the resolver entirely.

## D119 — Operational audit: attempt/completion pairs, checkpointed retention, offline verification

**Decision.** Every `tools/call` dispatched over HTTP or stdio now appends two audit events when
`audit.log` is configured (`internal/mcpserver/audit.go`, `Server.SetAuditLog`): an *attempt* before
the handler runs and a *completion* after it, carrying tool, KB, transport, principal and outcome.
The principal is read from the request context populated by D118, not passed as a separate argument.
`audit.mode` selects the failure semantics: `best_effort` (default) counts a failed append and lets
the call proceed; `required` rejects the call before the tool runs. Segments rotate into
`audit.archive_dir`, and `audit.retention_days` may delete a rotated segment only after it is
covered by a **signed checkpoint index**, so verification still succeeds for segments no longer on
disk. Appends are durable (`fsync`) with rollback on a partial write. `cartographer audit
verify|export` reads the files directly and never contacts a running server.

**Rationale.** A single event per call cannot distinguish "the operation did not happen" from "the
process died while it was happening" — the attempt/completion pair makes an interrupted operation
visible as an unmatched attempt, which is exactly the case a compliance review cares about. Both
modes are needed because they encode opposite priorities: most deployments must not lose
availability to a full disk, while a compliance deployment must never execute an operation it cannot
record, and only the caller knows which one it is. Deleting a segment would normally break the hash
chain, so retention is gated on the checkpoint rather than on age alone: the chain stays verifiable
without keeping every byte forever. Verification is deliberately offline because the moment an audit
trail is most needed is when the server is not running.

**Consequences.** `required` mode couples MCP availability to the audit sink's availability: an
unwritable log takes writes down. That is the intended trade, and it is why `best_effort` remains the
default so no existing deployment changes behaviour on upgrade. The two-event scheme roughly doubles
the log's line count and makes a naive `wc -l` over-report operations by 2×. `export` refuses to emit
a report for an unverifiable chain, so a corrupt log yields no document at all rather than a partial
one that would read as authoritative. The fault-injection seam used by the failure-path tests is
exported (`audit.FailAppendsForTest`) because the MCP layer lives in another package and its entire
contract is about what happens when appends fail; it is test-only and never reached in production
code. `audit.mode` and the rotation keys are YAML-only — the existing `CARTOGRAPHER_AUDIT_LOG`
environment variable still enables the log with default best-effort behaviour.
