---
topic: data-plane
---

# D5 — Normalized content-hash

`ContentHash` = sha256 hex of the normalized content: CRLF→LF, whitespace trim, removal of trailing empty lines, removal of `timestamp:` (auto-generated). Canonical ordering of YAML keys + per-section hashes (`SectionHashes`) since M2. Avoids spurious `stale_write` on non-substantive variations.
