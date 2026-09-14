---
topic: sync-provisioning
---

# D40 — `sync_pull` and client-side trust; anti path-traversal guard in `provisioning.Apply`

**Decision.** New MCP tool `sync_pull()` (read-only): returns the manifest with each artifact's
contents embedded in base64, so a remote HTTP client without a shared filesystem
can materialize locally. Unlike `sync_apply`, `sync_pull` does not accept
`auto_trust`: the trust decision is entirely client-side. Multi-provider lockfile v2
(`LockFile{Providers: map[string]Lock}`, automatic migration from v1). `provisioning.Apply`
gains an explicit guard: artifact name and file path must be `filepath.IsLocal`.
**Rationale.** The server must not decide trust on behalf of the remote client, which may have
a different policy — hence `sync_apply` (local, trust = tool parameter) vs `sync_pull`
(remote, trust decided by whoever materializes). The path-traversal guard is necessary because with
`sync_pull` the paths arrive as network data (JSON), not from an already trusted filesystem.
Details: `docs/sync.md`, `docs/control-plane.md` §Client synchronization ↔ provisioning.
