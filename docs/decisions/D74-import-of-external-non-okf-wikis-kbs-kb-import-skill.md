---
topic: data-plane
---

# D74 — Import of external non-OKF wikis/KBs: `kb-import` skill + CLI scaffold + lint-driven curation

**Status: implemented (2026-07-10).**

**Decision.** Importing an external corpus (Obsidian vault, markdown folder, wiki export) is an **agentic procedure** — bundled skill `kb-import` (`internal/skillbundle/bundled/kb-import/`), sister of `kb-create` — consistent with D28: no server-side ingest tool. Two legs: a mechanical **CLI scaffold** for mass conversion with no LLM consumption, and an incremental **agentic curation** driven by the lint.

**WP1 — `imported_draft` lint (warning).** A concept with frontmatter `status: imported` produces an `imported_draft` finding. Implementation next to `stale_claim` in `internal/lint/lint.go:131`; tests in `lint_test.go`. The marker makes the curation debt **visible and splittable across multiple sessions** instead of a big-bang.

**WP2 — `cartographer import` subcommand.** `cartographer import --source <dir> --kb <dir> [--archive <nome>] [--map <srcdir>=<archivio>] [--dry-run]`: walks the source's `.md` files; for each file it synthesizes the minimal frontmatter (title from the first H1 or from the filename, `status: imported`) preserving any existing frontmatter and adding only the missing fields; maps the source's relative directories onto archive/dossier; writes via `kb.Open`+`WriteConcept` (OKF invariants for free, no per-file commit: a single final commit by the operator). `--dry-run` prints the mapping plan without writing. Dispatch in `cmd/cartographer/main.go:73`.

**Links.** Wiki-links `[[...]]` remain valid as-is (D72, first-class in `ExtractLinks`); relative markdown links are rewritten best-effort by the scaffold against the new layout; `broken_link` acts as a safety net for what slips through.

**Deviations from the specification (implementation, 2026-07-10).**
- `kb.WriteConcept` requires a non-empty `type` (pre-existing OKF invariant, not discussed in D74's original text): when the source file has no `type` — neither pre-existing nor otherwise derivable — the scaffold synthesizes `type: Note` alongside `title`/`status`. A `type` already present in the source frontmatter is never touched.
- The `--map <srcdir>=<archivio>` mapping is **exact** per-directory: it matches only the literal source directory (relative to `--source`, `.` for the root), with no inheritance on nested subdirectories — consistent with WP2's "mechanical, not intelligent scaffold" spirit; subdirectories not explicitly mapped fall back to `--archive` (flat default) or, in its absence, fail the whole command with the list of unmapped directories, before any write.
- Slug collisions (two source files normalizing to the same destination concept ID) are resolved with an incremental numeric suffix (`-2`, `-3`, …) instead of failing the import.
- **Amended by D91.** The optional final commit, map scaffold, and directory-as-expanded-concept import behavior are specified in D91; the default remains an uncommitted, flat mechanical pass.

**Rationale.** The mechanical conversion (frontmatter, layout, links) must not consume LLM tokens — on a large wiki it would be unsustainable; the semantic part (mapping onto the archives, dedup, substantive rewrites) stays with the agent **with a human checkpoint on the mapping plan** before any write. Discarded alternatives: a server-side MCP import tool (re-creates the `source_ingest` removed with D28); fully agentic import (cost proportional to the corpus, not to the part requiring judgment).
