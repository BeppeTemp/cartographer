---
topic: deployment-release
---

# D112 — Reserved local endpoint defaults

**Status: implemented (2026-07-28).**

**Decision.** New local service configurations listen on `127.0.0.1:39273`,
and a new client uses `http://localhost:39273/mcp`. The dependency-light
`internal/defaults` package owns the port, listen address, and MCP URL so the
native-service and client first-run paths cannot drift. `cartographer serve`
without an explicit HTTP setting remains stdio.

**Compatibility.** Existing server YAML and `.cartographer.yaml` values,
including an explicit port 8080, remain authoritative and are never rewritten.
The normal precedence rules continue to apply: server flag > environment >
YAML > default; client existing YAML > `CARTOGRAPHER_SERVER_URL` > local
default.

**Rationale.** Port 8080 is frequently occupied by local development servers
and infrastructure. Reserving one project-local default avoids that collision
without making the endpoint a protocol requirement or migrating an operator's
chosen configuration.
