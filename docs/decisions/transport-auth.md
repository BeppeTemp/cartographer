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

## D120 — Tool-prefix discovery for client-owned multi-KB operations

**Decision.** `GET /health` now advertises each mounted KB's effective tool-name prefix
(`mcpserver.KBInfo.ToolPrefix`, populated at mount time), and every client-owned direct tool call is
qualified from that snapshot rather than from a locally recomputed prefix
(`resolveKBTargets`/`qualifyTool`/`callTool` in `cmd/cartographer/multikb.go`, used by `sync`,
`reindex` and the TUI). The discovered value is used live and never persisted. The TUI's MCP-config
badge becomes three-state — `in-sync`, `partial`, `missing` — computed against **all** expected
multi-KB entries instead of collapsing an incomplete configuration to `missing`. Remote failures
carry a typed taxonomy that separates a server that never answered from a server that answered with
a protocol or tool error, so the latter is no longer displayed as `unreachable`. This is a corrective
extension of D102: the prefix remains opt-in and default-off.

**Rationale.** D102 let an operator choose an arbitrary `tool_prefix`, but the client kept deriving
the namespace from the KB name. On any installation whose prefix was not exactly the sanitised KB
name, every client-owned call named a tool that did not exist. The symptom reached the operator as
two false diagnostics — `mcp-config missing` and `artifacts: server unreachable` — that pointed at
the network and the provider config while the server was healthy and correctly configured. Discovery
is the only sound fix: the prefix is server state, so the server must report it. Not persisting it
keeps client and server from drifting when a prefix changes. The badge and the error taxonomy are
part of the same defect: a diagnostic that misattributes a failure costs more than the failure.

**Consequences.** `/health` grows a field; the key is omitted when empty, so an older client parsing
the response is unaffected and an unprefixed deployment sees a byte-identical body. Every direct
tool call now depends on a successful `/health` first — a client that cannot reach health cannot
qualify a call, which is why an unreachable server is reported as exactly that and not as a tool
failure. The three-state badge means an operator who previously read `missing` on a partially
provisioned multi-KB setup now reads `partial`: same underlying state, but it no longer suggests
nothing was written. `server_url` in the client config is still expected to include the `/mcp` path
segment; `/health` is derived from it by stripping that segment, unchanged from before.

## D128 — Serve the 2026-07-28 revision alongside the handshake era

**Decision.** The server answers both protocol generations at once and decides which one applies per
request: a request is `2026-07-28`-era iff `params._meta.io.modelcontextprotocol/protocolVersion` is
present or the `MCP-Protocol-Version` header is (`Request.resolveEra`, `internal/mcpserver/protocol.go`),
everything else is the handshake era. The era's result envelope (`resultType: "complete"` plus
`_meta.io.modelcontextprotocol/serverInfo`) is applied in exactly one place, on the way out of
`dispatch`, so no handler knows about eras; `tools/list` additionally carries `ttlMs` and
`cacheScope: "private"` in the new era. `server/discover` is implemented and answers in both eras;
`initialize` and `ping` stay routed for the handshake era. Over HTTP the three mirror headers are
validated against the body (`-32020`, HTTP 400), an unsupported version is `-32022` + 400, an unknown
method is 404 in the new era and 200 in the handshake era, and `GET`/`DELETE` on the MCP endpoint are
405. `Origin` is validated on the MCP endpoint against an operator allow-list that defaults to
same-origin, before authentication runs, and the CORS allow-origin header echoes the accepted origin
instead of `*`. The `resources` capability, which no method ever backed, is no longer advertised.
The CLI client's reachability probe moves from `ping` to `server/discover` and now surfaces the
JSON-RPC error carried by a 400 or 404 instead of the bare status.

**Rationale.** What `2026-07-28` removes — the handshake, sessions, SSE resumability — is what this
server never had, so the architecture already matched the new model and only the protocol surface
lagged. Flipping the version outright was not defensible: the actual consumers are Claude Code,
Codex, Kiro and OpenCode, and it is not established that any of them speaks the revision yet, so a
flip would have broken every live KB client to satisfy a spec no client had asked for. Serving both
is cheap precisely because the server is stateless — the era is a property of a request, not of a
connection, so coexistence costs no bookkeeping. The Go SDK was rejected: adopting it would mean
rewriting `dispatch`, the policy gate, the audit wrapping and the tool-prefix machinery, all built
around the hand-rolled `Request`/`Response`, to close a conformance delta of a few hundred lines —
and it would add this project's first protocol dependency. The mirror headers are validated rather
than trusted because they are client-controlled and duplicate what the body already says; letting an
authorization decision read them would create exactly the split source of truth the spec mandates
the validation to close. `Origin` validation has been a MUST since `2025-03-26` and was simply
missing; a wildcard allow-origin alongside it is what turns a rebinding attack into a readable
response.

**Consequences.** Existing clients are unaffected: a handshake-era request produces byte-identical
responses, which is asserted directly (`TestHandshakeEra_ResponsesUnchanged`) and indirectly by the
e2e suite passing unedited. Operators who expose the server to browsers must now set
`mcp.allowed_origins`, or accept the same-origin default — the one behaviour change that can turn a
working setup into a 403, which is why `"*"` exists as an explicit opt-out. The same-origin default
is not by itself a rebinding defence, since a rebound name matches both `Origin` and `Host`; the
allow-list is. `resources` disappearing from the capability map costs nothing observable, since a
client acting on it got `-32601` anyway. `skills.listChanged` stays, because `notifyWrap` and
`artifactNotifyWrap` do emit that notification — an earlier reading that called it unbacked was
wrong. The CLI client deliberately stays handshake-era, `_meta`-free: the server serves both, and
moving the client only pays off once the handshake era is retired, which is deferred to D130 and
gated on the per-request era roster of D129.

## D129 — Report the protocol era and client identity of connected clients

**Decision.** Each `*Server` keeps a bounded in-memory tally of the clients that reach it, keyed by
client name, client version, protocol version and era (`internal/mcpserver/clients.go`). Identity is
read from `_meta.io.modelcontextprotocol/clientInfo` in the `2026-07-28` era and from
`initialize`'s `params.clientInfo` in the handshake era — one shared `parseClientInfo`, called from
`resolveEra` and `handleInitialize`. A handshake-era request that is not `initialize` carries no
identity and is counted under the literal client name `unknown`. Recording happens in
`dispatchMethod` after the metadata authorization check, so a denied caller records nothing, and for
`initialize` after the handler has parsed `clientInfo` and settled the negotiated version. The tally
is capped at 64 distinct keys with each identity field truncated to 64 bytes (rune-safe); beyond the
cap a single `overflow` counter increments and existing keys keep counting. It is exposed by
`GET /clients` on all three handler constructors (`HTTPHandler`, `FullHTTPHandler`, the multi-KB
`Handler`), as one flat array across mounted KBs sorted by (kb, client name, version), 405 on any
method other than GET, `{"clients":[]}` and 200 on a fresh server. `/clients` is not added to
`isPublicPath`, so it inherits the bearer requirement.

**Rationale.** D128 kept the handshake era alive precisely because it is not established which
revision the real clients speak, and D130 cannot be scheduled on a guess: retiring an era needs
evidence that every client of a deployment has moved. That evidence was unobtainable — `initialize`
read `protocolVersion` into a local variable and dropped `clientInfo` entirely, and the audit log
covers only `tools/call`, so the one signal needed was structurally outside it. The audit log was
rejected as the home for this anyway: it is a signed hash chain, not the place for a soft
operational counter. The endpoint is separate from `/health` because `/health` is deliberately
exempt from auth for k8s probes, and client names and versions are deployment topology; extending it
would have published them unauthenticated. No MCP tool was added: this is operator-facing, an agent
has no use for the client roster of its own server, and every tool costs context in every KB's
`tools/list`.

**Consequences.** The question "which clients talk to this server, on which protocol version" is now
answerable on any deployment, which is what unblocks D130 — and it recurs on every future revision,
not just this one. The data is process-local and lost on restart, deliberately: durable history is
the audit log's job. Identity stays self-reported and unverified, and must never become an
authorization input — stated here so nothing is later built on it. D128's "no session state"
invariant holds: the tally is aggregate observability keyed by client identity, never by connection;
it mints no identifier and is never echoed to a client, so two requests from the same client remain
indistinguishable to the protocol. Recording is a side effect on the request path with no failure
path — hitting the cap is invisible to the caller. A hostile client can burn the 64 key slots, which
degrades the roster to an overflow count but cannot grow memory; an operator who sees `overflow`
climb should read it as noise, not as topology.

<a id="d132"></a>
## D132 — Audit authorization denials; serve RFC 9728 metadata; drop the unwired HTTP handler layer

**Decision.** Three corrections, found while implementing D128 (issue #122, outside its scope).
(1) `handleToolsCall` (`internal/mcpserver/server.go`) returned as soon as `s.authorize` failed,
before `beginAuditCall` ever ran — a denial produced no audit event at all, even though D119 already
documents the attempt+completion pair with `outcome=unauthorized` as part of the contract. The denial
is now audited at the point the decision actually happens: on the `authorize` failure path,
`handleToolsCall` calls `Server.auditDenied`, which now takes the principal directly (already resolved
from the request context per D118) instead of an `*http.Request` — the dependency existed only to
re-derive the principal, redundantly, from the bearer token. (2) `docs/transport-auth.md` already
claimed the server publishes RFC 9728 Protected Resource Metadata, and `auth.go`'s `isPublicPath`
already exempted `/.well-known/oauth-protected-resource` from authentication, but nothing served it —
`auth.ProtectedResourceMetadata` had no caller outside its own test. The route is now registered in
`MultiKBServer.Handler()`, the handler `cmd/cartographer/serve.go` actually wraps in
`OriginGuard(store.Middleware(...))`; `resource` and `authorization_servers` both name this server's
own base URL, reconstructed per-request from `Host` and `X-Forwarded-Proto`, since there is no
separate OAuth authorization server to plumb through config (this server validates its own static
tokens). (3) With both fixed, `mcpAccessGuard` and the exported `FullHTTPHandler`, `WellKnownHandler`,
`ListenAndServeWithHandler` and `ListenAndServeHandler` (`internal/mcpserver/httpserver.go`) had no
caller in production or in tests — `auditDenied` was `mcpAccessGuard`'s only caller, and
`cmd/cartographer/serve.go` has always built its handler directly from `MultiKBServer.Handler()`,
never through any of the five. All five are deleted; the handful of tests that only exercised
`FullHTTPHandler` as a third member of a "same behavior across handler constructors" table lost that
one entry, not the test.

**Rationale.** An audit log exists precisely to show what was refused, and `mcpAccessGuard`'s
per-KB r/rw body-peek (D45) and `service_get`/`resolve_secrets` override (D47) had already been
re-implemented in the authorizer (`internal/mcpserver/policy.go`, D118) — nothing was unenforced, but
three comments elsewhere still pointed at the guard as the place access is decided
(`audit.go`, `tools_skill.go`), so a reader looking for the enforcement point found the wrong function
first. Serving the RFC 9728 route was the smaller of the two options available (serve it, or retract
the docs claim and the auth exemption): the docs and the exemption already existed and were correct in
intent, only the handler was missing. Deriving `resource`/`authorization_servers` from the request
rather than adding a config field keeps the change to the size the docs already promised, and is
honest about what this deployment actually is — its own token validator, not a client of a separate
authorization server.

**Consequences.** A denied `tools/call` now appears in the audit log exactly like every other call,
closing the gap between D119's documented contract and what the code did. The RFC 9728 endpoint is
self-describing rather than operator-configured: an operator behind a reverse proxy that does not set
`X-Forwarded-Proto` gets an `http://` `resource` even when the public entry point is HTTPS — no worse
than the previous 404, but not authoritative either; a future need for a precise external issuer
would need real config, not this inference. Deleting the dead handler layer removes the only place
`FullHTTPHandler`'s CORS behavior was exercised at all outside its own tests — CORS for the production
path continues to live in `OriginGuard`, unaffected by this change. `Server.HTTPHandler` (single-KB,
no `.well-known` route) and the plain `Server.ListenAndServe` are unaffected: neither is on the
production path (`serveHTTP` always mounts through `MultiKBServer`, even for one KB) but both remain
for direct single-KB embedding and their own tests, which this issue did not ask to remove.
