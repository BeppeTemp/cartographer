---
topic: control-plane
---

# D1559 — The SDK's localhost guard is switched off; our own guards own the Host rule

**Decision.** The Streamable HTTP handler is built with `DisableLocalhostProtection: true`. The go-sdk default answers a plain-text 403 to any request that reaches a loopback listener with a non-loopback `Host`. The Host and Origin rule is enforced by `OriginGuard` (same-origin, D906) and, for a loopback listener with authentication off, `LoopbackHostGuard` (D268); with authentication on, the token is the access control.

**Why.** The SDK guard fires with a valid token too, so every same-host reverse proxy that keeps the public `Host` (Caddy, nginx, an auth sidecar in the same pod) got a 403 on `POST /mcp*` while `/health` and `/api/ui/v1` kept working, which hid the fault. It contradicted `docs/transport-auth.md` §Origin and answered in plain text where the transport answers JSON-RPC. It costs one more place that must be right: if `LoopbackHostGuard` were not installed for a loopback, auth-off server, nothing else would stop a DNS-rebinding page.

**Alternatives rejected.**
- Keep the SDK guard and document it: it breaks the proxy setups the documentation recommends unless each rewrites `Host`.
- Keep it and make it answer in JSON-RPC: a third guard with a different rule, still refusing an authenticated request.

**Consequences.** The only DNS-rebinding defence of an auth-off loopback server is `LoopbackHostGuard`, pinned by a test that sends a foreign `Host` to a real loopback listener. A library default that is switched off must be named here and replaced by a guard we test.
