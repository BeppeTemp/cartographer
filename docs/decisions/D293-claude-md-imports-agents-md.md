---
topic: sync-provisioning
---

# D293 — A project CLAUDE.md written by sync imports AGENTS.md

**Decision.** When Cartographer writes a managed instructions block into a
project-scope `CLAUDE.md` and the project root contains an `AGENTS.md`, the
block starts with an `@AGENTS.md` import line. The import lives **inside** the
markers, so `removeInstructionsBlock` drops it with the block and a
Cartographer-created file still disappears on unbind.

**Why.** Since Claude Code 2.1.277 the built-in `agents-md@builtin` plugin
(enabled by default) reads a project's `AGENTS.md` — but **only when no
`CLAUDE.md` or `CLAUDE.local.md` is on the project path**. Creating a
`CLAUDE.md` to hold the managed block therefore silently hides the repository's
own instructions from every Claude session in it. An `@AGENTS.md` import inside
that file restores visibility without touching content outside the block.

Probed on Claude Code 2.1.286 (2026-10-01, temp git repo, plugin enabled):

| Project files | AGENTS.md visible |
|---|---|
| `AGENTS.md` only | yes |
| `AGENTS.md` + `CLAUDE.md` with managed block only | **no** |
| `AGENTS.md` + `.claude/CLAUDE.md` with managed block | **no** |
| `AGENTS.md` + `CLAUDE.md` with `@AGENTS.md` + managed block | yes |

**Alternatives rejected.**

- *Move the block to `.claude/CLAUDE.md` instead of `./CLAUDE.md`.* Probe row 3
  shows this still hides `AGENTS.md`: Claude checks both paths. No gain.
- *Do not write the file at all when `AGENTS.md` exists.* This sacrifices the
  instructions block for every project that follows the `AGENTS.md` convention —
  the most common shape — and the agent loses the KB's operational instructions
  (archives, placeholder table, per-KB directives) in those sessions.
- *Write the import outside the block.* The block invariant is that Cartographer
  never edits content outside its markers. An import outside would not be removed
  on unbind, leaving a stale reference in the user's file.

**Consequences.** The import is re-evaluated on every sync: an `AGENTS.md`
added later gets the import, one removed loses it (ordinary block-content
drift, no special state). A repository whose `CLAUDE.md` already contains an
`@AGENTS.md` import outside the block (D213 convention) does not get a
duplicate. The user-scope `~/.claude/CLAUDE.md` is unaffected.
