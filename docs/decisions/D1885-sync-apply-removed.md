---
topic: sync-provisioning
---

# D1885 — the server never writes a client's directory; `sync_apply` is removed

**Decision.** The `sync_apply` MCP tool is removed. `cartographer sync` on the machine whose files they are is the only materializer; it reads the manifest through `sync_pull`. A call to `sync_apply` answers the ordinary tool-not-found text (D151) plus a pointer from the `removedTools` table (`visibility.go`): `cartographer sync` for a connection addressed to an operator (D1101), `sync_pull` otherwise.

**Why.** It ran `provisioning.Apply` on the server host and wrote a caller-named `base_dir`: the server acting as a client, the only reason `mcpserver` linked the apply half of `provisioning`, and a tool that every token was refused anyway (local operator only). No code in `cmd/` called it. It cost a local-only authorizer path and a second implementation of the apply flow to keep aligned with the client one. It was released from v0.1.0 to v0.28.0, so removal is breaking for a third-party client that calls it (`feat!`).

**Alternatives rejected.** Keeping it hidden: it keeps the server linking the apply half and the local-only gate. Keeping a stub that answers with a pointer as a registered tool: it would show in `tools/list` and in the tool table for a tool that does nothing. A generic "removed" answer for every absent name: D151 keeps the text identical for every absent tool, so only names in the `removedTools` table are told apart.

**Consequences.** `auth.Principal.Local` stays (used elsewhere); the local-only deny code is gone. The Surveyor deny list keeps the name `sync_apply`: it is a "never" list, not a registry, and a Surveyor newer than its server (or older than the removal) must still never offer it. The apply half of `provisioning` is now reached only from the client; the package split follows in later changes.
