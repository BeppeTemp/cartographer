---
topic: client-configurator
---

# D92 — Per-KB MCP entries for multi-KB servers

**Status: implemented (2026-07-24).**

**Decision.** `connect` and `sync` enumerate mounted KBs from `GET /health`. A multi-KB server
emits one entry per KB, named `<server_name>-<kb>` and scoped with `?kb=<kb>`; the discovered list
is persisted in `.cartographer.yaml` and `sync` reconciles additions, removals and the
one↔many rename. A one-KB or older server retains the single bare `<server_name>` entry. Disconnect
removes both the bare entry and every persisted suffixed entry.

**Rationale.** Query routing already works on every server version that can mount multiple KBs,
whereas path routing is newer and not required by all clients. Separate agent-visible entries make
the KB choice explicit and prevent a second mounted KB from turning an existing bare `/mcp` entry
into a 400, without introducing a client-side multiplexing protocol.
