---
topic: sync-provisioning
---

# D105 — Binary-safe provisioning artifacts and executable KB scripts

**Decision.** Provisioning artifact content is raw bytes; MCP read/write uses explicit `text`/`base64`
encoding and retains raw-byte `sha256` for `if_match`. Executability is derived from the KB filesystem,
not a descriptor. Hooks impose an effective mode: every file except `hook.json` is executable, while
`hook.json` is always non-executable. The versioned, domain-separated artifact hash includes that
effective mode.

**Rationale.** Git already versions the executable bit, so the KB remains the sole registry rather
than requiring a parallel metadata file. Including the effective mode in the hash makes `chmod` change
the manifest revision and realign existing installations; normalizing the hook floor avoids drift from
raw mode changes that cannot affect the materialized result. Base64 prevents byte corruption while
preserving the existing text API and `if_match` contract.
