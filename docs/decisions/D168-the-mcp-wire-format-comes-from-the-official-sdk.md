---
topic: transport-auth
---

# D168 — The MCP wire format comes from the official SDK

**Status: implemented (2026-09-05).** Supersedes the implementation half of
[D128](D128-serve-the-2026-07-28-revision-alongside-the-handshake.md) and [D133](D133-the-version-header-selects-the-era-by-value-not-by.md); closes the need for the plan in issue #118.

**Context.** Cartographer implemented the MCP wire format itself: the JSON-RPC
envelope, both protocol eras and the rules for telling them apart, header mirroring
and its validation, version negotiation, the cacheable-result fields, the stdio
read/write loop. That was defensible when it was ~340 lines against one revision. It
stopped being defensible for two reasons at once.

The first is maintenance. The specification moves, and every revision arrived as a
diff in our code plus a diff in the byte-for-byte tests that pinned it. The second is
that the cost had become **unpayable rather than merely high**: [D128](D128-serve-the-2026-07-28-revision-alongside-the-handshake.md) made the
server answer two eras so no client would be stranded, and issue #118 — the plan to
retire the old one — was written, implemented on a branch, and then blocked, because
its entry condition requires every supported provider to have migrated and three of
the four had not. The duality was ours to carry indefinitely, on someone else's
schedule.

The official [Go SDK](https://github.com/modelcontextprotocol/go-sdk) serves
`2024-11-05` through `2026-07-28` and decides the era **per request**, from `_meta` or
the mirror headers — which is D128's design, arrived at independently. Adopting it
ends the duality without retiring anything and without waiting for any provider.

**Decision.** The SDK carries the protocol; `internal/mcpserver/sdkbridge.go` is the
seam. The tool registry stays the source of truth and tools are wrapped onto the SDK
rather than defined against it, because what sits above the wire is not protocol:
per-tool authorization ([D118](D118-fine-grained-rbac-and-permission-aware-retrieval.md)), the audit pair ([D119](D119-operational-audit-attempt-completion-pairs.md)), the client
roster, the agent profile hiding advanced tools from `tools/list` while leaving them
callable, tool-name prefixes ([D102](D102-opt-in-per-kb-mcp-tool-name-prefix.md)), and [D151](D151-a-kb-s-capabilities-and-mount-provenance-are-visible.md)'s
informative unknown-tool message. `callTool` is the single path a tool call takes,
shared by the SDK handler, the unknown-tool middleware and the tests.

Transport options are not tuning knobs: `Stateless` and `JSONResponse` reproduce the
contract this server has always documented — every POST self-contained, no session id,
one complete JSON response.

**Rationale.** Writing the bridge over the registry rather than registering tools
directly on an `sdk.Server` was the whole design question. Registering directly would
have been shorter and would have put the SDK in charge of things it has no opinion
about — a tool hidden from a listing but still callable, a canonical name distinct
from the registered one, an unknown tool that must explain *which kind* of unknown it
is. Those are Cartographer's rules, and a seam is what keeps a future SDK release from
being able to change them.

The dependency was the case against, and it is real: eight modules in a project whose
convention is stdlib-first, one of which hand-writes a YAML parser to avoid a
dependency. It is taken anyway because this is the inverse of a normal dependency —
it is not new capability, it is the removal of an obligation to keep re-implementing a
specification we do not control. Six modules actually enter the server build.

**Consequences.** Five behaviour changes, each deliberate:

- **A refusal of protocol metadata is a JSON-RPC error**, where it used to be a success
  envelope carrying `isError`. `isError` is a `tools/call` concept and the result types
  of `tools/list` and `server/discover` have nowhere to put it; dressing a refusal as a
  successful *empty tool list* is precisely how one went unnoticed — an old test
  asserted only HTTP 200 and passed while being refused. A `tools/call` refusal is
  unchanged.
- **stdio is a session.** The client owns the pipe and the server tears down on EOF, so
  `echo … | cartographer serve` now races its own response; `make smoke` holds stdin
  open. The old synchronous loop made that shortcut work by accident. Requests on one
  session are also served concurrently now.
- **`server/discover` is `2026-07-28`-only**, and every POST must send `Accept` naming
  both `application/json` and `text/event-stream`. The released CLI sent neither, so
  the client moved to the new era in the same change — issue #118's WP1, which turns
  out to be required rather than optional. *The `Accept` requirement is relaxed by
  [D200](D200-a-post-s-accept-header-is-supplied-not-enforced.md).*
- **`notifications/skills/list_changed` is removed.** It was non-standard, reached only
  stdio clients, and the SDK exposes no API for arbitrary notifications — stateless
  HTTP has no server→client channel at all. A `Notify` that silently did nothing would
  be worse than its absence.
- **Client identity is per session**, agreed once at `initialize`, where it used to be
  readable per request.

`protocol.go` goes from 342 lines to 109 and `server.go` from 674 to 314; the 414-line
suite that pinned the era machinery byte for byte is replaced by a much smaller one
asserting only what Cartographer still owns. Issue #118 can be closed as overtaken:
the cost it existed to remove is gone, and the removal it proposed — which would have
stranded every un-migrated provider — is no longer necessary.
