---
topic: transport-auth
---

# D16 — HTTP transport: hand-rolled Streamable HTTP

`net/http` handlers: `POST /mcp` (JSON-RPC), `GET /health`, `/.well-known/oauth-protected-resource` (RFC 9728). CORS headers. No SSE streaming for now (each request = one complete response).
