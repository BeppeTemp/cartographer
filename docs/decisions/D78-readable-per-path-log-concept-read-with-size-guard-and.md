---
topic: control-plane
---

# D78 — Readable per-path log, `concept_read` with size guard and outline, `concept_oversize` lint

**Status: implemented (2026-07-14).**

**Context.** Real-use analysis: (1) `log_append(path=...)` prefixes `[<path>] ` and always writes to the root log (an architectural TODO never resolved), but `log_tail(path=...)` read `<path>/log.md` — nonexistent — silently returning empty: the entries existed but were invisible to the agent. (2) A real 92 KB concept blew through the MCP client's token budget on `concept_read`, forcing a workaround outside the MCP surface (direct file read). No guard prevented either the full read of a huge body or a concept's growth beyond a reasonable threshold.

**Decision.**

a) **Root-log-with-prefix confirmed** (the per-directory log remains deferred, YAGNI): `log_tail(path)` now reads any entries of `<path>/log.md` (rare, pre-existing files) followed by the root log entries whose text starts with `[<path>] `, up to the overall `n` limit. The tool never silently returns an empty string: no entries → JSON note `{"entries": 0, "note": "..."}`.

b) **Size guard on `concept_read`**: body beyond `conceptReadSizeGuard` = 60000 bytes (`internal/mcpserver/tools_read.go`) with neither `section` nor `outline` → response as an outline (`{id, content_hash, outline: [{level, title, bytes}], body_bytes}`) plus a `note` explaining to use `section` or `full: true`. `full: true` always forces the full content. New helper `okf.ListHeadings` (same section semantics as `ExtractSection`) feeds both the outline and the "section not found" error, which now lists the available headings (cap 50) instead of leaving the agent to guess.

c) **`concept_oversize` lint** (`info` severity, like `map_oversize`): body beyond `conceptOversizeThreshold` = 30000 bytes (`internal/lint/lint.go`) flags the concept as a `concept_expand` candidate — the lint threshold is lower than the read one to anticipate the problem before it becomes a blocker.

**Rationale.** The three thresholds share the same principle: the KB must defend itself from its own growth instead of relying on the agent's discipline (same spirit as D77). The broken log-tail was a pure bug (asymmetry between `log_append`/`log_tail`), not a new design choice.
