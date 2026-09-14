---
topic: transport-auth
---

# D133 — The version header selects the era by value, not by presence

**Decision.** `Request.resolveEra` switches to the `2026-07-28` era from the
`MCP-Protocol-Version` header only when that header's value is exactly
`2026-07-28`. Any other value — an earlier revision, or one this server does not
know — leaves the request in the handshake era. The
`_meta.io.modelcontextprotocol/protocolVersion` path is unchanged: a client that
sets that reserved key is declaring the new revision, so an unsupported value
there still yields `-32022`.

**Context.** D128 keyed the era off the header's *presence*
(`if headerVersion != ""`). That silently assumed only a `2026-07-28` client
would ever send it, which is wrong: `MCP-Protocol-Version` has been part of the
HTTP transport since `2025-06-18`, so a conformant client on an earlier revision
sends it while sending none of the mirrored headers `validateMirrorHeaders`
demands. Such a client was classified into the new era and then rejected with
`-32020 missing required header Mcp-Method` — and had it passed that check it
would have hit `-32022`, since `SupportedProtocolVersions` does not list the
intermediate revisions. Found on `v0.6.0` against a real client, reported as
issue #126.

**Rationale.** This restores D128's own stated invariant — a handshake-era
client sees byte-identical responses to what it saw before `2026-07-28` was
served — which the presence check violated for every client that sends the
header. Before D128 the header was not read at all, so falling back to the
handshake era on any other value *is* the pre-D128 behaviour, not a new policy.
Keying on the exact value is also the narrowest fix available: it needs no list
of known earlier revisions to maintain, and it leaves the new era's own
requirements untouched — a client naming `2026-07-28` without the mirror headers
is still correctly rejected.

**Consequences.** A client sending an unknown or future version in the header is
served the handshake era rather than told its version is unsupported. That is
the right trade while both eras are served, but it stops being so once the
handshake era is retired: D130 (#118) already specifies that afterwards any
version other than `2026-07-28` gets `-32022` with a single-entry list, and this
branch is one of the places that plan must delete. Noted here so the retirement
does not have to rediscover it.
