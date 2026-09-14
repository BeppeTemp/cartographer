---
topic: sync-provisioning
---

# D115 — MCP allow-list and hash-bound local approval

**Decision.** A KB-provided HTTP MCP descriptor is exposed only when an exact
per-KB operator allow-list entry matches its name, transport and normalized
absolute target URL. Empty policy denies all. A client then materializes an
unsigned descriptor only when a local record binds its source KB, artifact name
and full content hash; generic trust never applies. Cryptographic verification
remains independent and is sufficient on the client, but cannot bypass the
server allow-list.

**Rationale.** The server controls which endpoints a KB may advertise; the
client controls consent to execute one exact descriptor. Hash-binding covers
headers and environment references as well as URLs, and keeps either grant
from silently authorizing a changed endpoint.
