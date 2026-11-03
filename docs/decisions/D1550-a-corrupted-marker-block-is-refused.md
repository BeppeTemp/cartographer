---
topic: sync-provisioning
---

# D1550 — A corrupted marker block is refused, never edited

**Decision.** A managed marker block (the instructions block in `CLAUDE.md`/`AGENTS.md`, the Codex `config.toml` blocks, the `info/exclude` block) is located by whole marker lines and is sound only when it is exactly one begin line followed by one end line. Any other shape (a begin without end, two begins before an end, an end before the begin) is corrupted: every writer leaves the file byte-identical and reports a per-file error or per-artifact refusal that points to `doctor`. It never appends a second block and never cuts. Marker openers quoted inside a KB's instructions text are rewritten (`cartographer-quoted:`) before the block is written, deterministically, so a rewrite converges.

**Why.** The old locator took the first begin and the first end after it. With an orphan begin it appended a second block, and the next rewrite cut from the orphan to the new end, deleting the user's text between them; a quoted end marker left the old tail outside the block. These files are the user's: refusing costs a manual fix, cutting costs their text.

**Alternatives rejected.**
- Repair the block automatically (drop the orphan): guesses which text is ours.
- Reject a KB body that quotes a marker: stricter, but drops that KB's instructions block for a documentation page.
- Keep one copy per writer: it was three copies of the same defect.

**Consequences.** One locator, `blocktext.ReplaceBlock`; a new marker-block writer uses it. A user file that legitimately quotes a marker line on its own line is refused until fixed. `internal/kb/genindex.go` still uses the legacy `ReplaceBetween` and the provenance stamp keeps `replaceBlock`; they are separate follow-ups. The `info/exclude` writer adopts the same rule in its own change.
