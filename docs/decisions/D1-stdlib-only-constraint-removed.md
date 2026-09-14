---
topic: project-governance
---

# D1 — Stdlib-only constraint ✅ removed

In the initial sandbox environment `go get` failed due to a MITM proxy: the M1–M3 code used stdlib only. The constraint has been removed. The hand-rolled implementations (YAML parser, MCP server) can be replaced with libraries when it makes sense.
