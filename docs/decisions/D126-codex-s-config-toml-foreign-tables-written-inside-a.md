---
topic: client-configurator
---

# D126 — Codex's `config.toml`: foreign tables written inside a managed block are relocated, not lost

**Status: implemented (2026-08-04).**

**Decision.** Before either Codex `config.toml` block rewrite, Cartographer moves out of the
span every top-level table group that the block's next contents do not themselves declare
(`configurator.EvictForeignTablesFromBlock`, `internal/configurator/codextoml.go`, sibling of
D99's `AdoptCodexOrphanTables`). Foreign groups are cut from the span, verbatim, and re-inserted,
in file order, immediately before the begin marker line, separated from their neighbours by at
most one blank line (`joinSeam`); a key declared both inside the span and in the block's next
contents is left alone — it is ours, `blocktext.Write` will overwrite it. Both call sites
(`registerHookConfigTOML` in `internal/provisioning/hooksettings.go`, and the `.toml` branch of
`configurator.Apply`) run the new eviction *before* D99's adoption and before `blocktext.Write`,
so a table that is both foreign-in-span and adoption-owned collapses to one copy instead of a
duplicate key. Purely textual, as D58 requires: no parse/re-serialize, every byte outside the
moved spans preserved.

**Rationale.** D99 made the write path idempotent against Codex's own rewrites by adopting the
marker-less copies of tables Cartographer owns. It did not cover the mirror case: Codex records
its per-hook trusted-hash bookkeeping (`[hooks.state."<config path>:<event>:<i>:<j>"]`) positionally
after the last `[[hooks.*]]` table in the file — which, in a Cartographer-managed `config.toml`, is
the one inside our own span. `blocktext.Write` replaces everything between its markers, so those
tables were silently destroyed on the next `connect`/`sync`, even though `codexHookTableOwner`
(D99) deliberately excludes `[hooks.state]` from adoption specifically to leave Codex's own
bookkeeping alone — the write path was destroying what the adoption predicate was written to
protect. Rewriting the relocated tables in canonical form would mean parsing/re-serializing
`config.toml`, which D58 forbids; relocating them verbatim keeps the invariant that Cartographer
owns only the text it wrote.

**Consequences.** Codex no longer loses the trust record for hooks it had already approved when
the state table happens to fall inside a managed block. `EvictForeignTablesFromBlock` reports each
relocation as its own warning, mirroring D99's phrasing. The fix lives in `internal/configurator`
next to the existing `codextoml.go` line/header/span primitives it reuses, not in `internal/blocktext`
— that package stays provider-agnostic (it also serves the `AGENTS.md`/`CLAUDE.md` instruction
block).
