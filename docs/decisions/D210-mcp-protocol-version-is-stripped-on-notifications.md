---
topic: transport-auth
---

# D210 — `Mcp-Protocol-Version` is stripped on notifications

**Status: implemented.** Phase 2 of #273. Closes #276.

**Context.** D200 solved Antigravity's `Accept`-header problem (phase 1). Phase 2 is a second SDK
rejection that appears once both sides negotiate `2026-07-28` (SEP-2575): Antigravity's Go MCP
client (`go-sdk` v1.7.0) attaches `Mcp-Protocol-Version: 2026-07-28` to every HTTP POST, including
`notifications/roots/list_changed`. The SDK server (`streamable.go:1509-1540`) enforces that when
the header names `2026-07-28`, the body must carry
`_meta.io.modelcontextprotocol/protocolVersion` matching it. JSON-RPC notifications carry no `_meta`
— the SDK's own `protocolVersionFromMessage` explicitly returns `""` for them — so the server
returns HTTP 400 with `-32602 missing or invalid _meta field`. Antigravity interprets the 400 as a
transport failure and drops the MCP connection, preventing all tool discovery.

**Decision.** `Server.handleMCP` sniffs the body of a POST whose `Mcp-Protocol-Version` header
names `2026-07-28` or later: if the body is a JSON-RPC notification (has `method` but no `id`),
the header is removed from the cloned request before passing it to the SDK. The SDK then treats
the notification as legacy-era and answers 202 Accepted, which is the correct response for a
fire-and-forget message.

- **Body sniffing, not full parse.** Only the top-level `id` and `method` keys are decoded — enough
  to distinguish a notification from a request, without unmarshalling the full params tree.
- **Bounded, and the body is never buffered whole.** The sniff reads at most
  `notificationSniffLimit` (8 KiB) and hands the SDK an `io.MultiReader` of the prefix and the
  unread remainder, so the request streams downstream exactly as before. A body that does not fit
  in the prefix is not a notification worth special-casing — the real one is 74 bytes — and the
  header is left alone. Reading the whole body with `io.ReadAll` would have been simpler and would
  have made every POST from a modern client hold its entire body in memory: this server sets no
  `MaxBytesReader` anywhere, so that is an unbounded allocation on an unauthenticated path.
- **Scope is narrow.** The header is stripped *only* when the body is a notification — regular
  requests with `_meta.protocolVersion` keep the header and are served as `2026-07-28` as before.
  This preserves the SDK's SEP-2575 validation for all non-notification messages.
- **Same leniency pattern as D200.** The server normalises what the SDK would reject, on a clone of
  the request, without changing what is returned or what well-formed clients experience.

**Alternatives rejected.**

- *Fix it in the SDK and wait.* The right long-term answer, and it does not help a user whose
  Antigravity cannot discover a single tool today. The workaround is written so that an SDK fix
  makes it a no-op rather than a defect: it would then strip a header the SDK never checks.
- *Answer the notification ourselves before the SDK sees it.* It moves protocol handling back out
  of the SDK, which is the thing D168 exists to stop doing.
- *Strip the header on every POST.* It would disable SEP-2575 validation for real requests, which
  is the check that catches a client whose header and body disagree (D133).
- *Sniff by method name* — treat `notifications/*` as notifications. The absence of `id` is what
  JSON-RPC actually defines, and the naming convention is not enforced by anything.
- *Buffer the whole body with `io.ReadAll`.* See above: correct, and it changes the server's memory
  profile for every request in order to inspect a message that is always small.

**Consequences.** Notifications under `2026-07-28` lose their era identity at the transport layer —
the roster records them as legacy rather than new-era. Since notifications produce no response and
are not tracked by the roster (they carry no `_meta` identity), this has no observable effect. The
comparison that decides "2026-07-28 or later" is a lexicographic one on the header value, so a
malformed value sorting above `2026-07-28` also gets the leniency; that is deliberate — it only ever
loosens a check on a body with no `id`, and any such request is rejected by the SDK on its own terms
anyway.
