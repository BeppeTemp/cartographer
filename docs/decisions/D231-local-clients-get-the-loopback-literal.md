---
topic: sync-provisioning
---

# D231 — Local clients get the loopback literal

**Decision.** The default MCP URL a client is provisioned with is
`http://127.0.0.1:39273/mcp`, derived from the service's default listen address
so the two cannot drift. A client config carrying the previous default origin,
`http://localhost:39273`, is read back on `127.0.0.1` with its path kept, and
the next `connect` or `sync` rewrites every provider's MCP entry from it. Any
other URL is left as written.

**Why.** The service listens on IPv4 loopback only (D112), while clients were
told `localhost`. On Windows `localhost` resolves to `::1` first, where nothing
listens: measured on Windows 11, a .NET request to `http://localhost:39273`
spent about 2 s falling back to IPv4 against 6 ms for the literal, and a client
without a fallback — Kiro CLI, reported in #329 — failed outright. macOS and
Linux fall back fast enough to hide it.

**Alternatives rejected.** Also listening on `[::1]:39273`: it doubles the
listening surface to fix a naming problem. Leaving existing installs to be
edited by hand: the broken value was the one Cartographer itself wrote, so
Cartographer rewrites it. Rewriting any `localhost` URL: a user who chose
another port or host made a choice, not a default.

**Consequences.** The migration keys on the exact origin, case-insensitively,
and keeps per-KB paths such as `/mcp/<kb>`. It happens when the client config
is loaded, so every command that loads it sees the new URL and the file itself
is updated the next time the client saves it.
