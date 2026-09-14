---
topic: data-plane
---

# D162 — Authoring and ingestion papercuts: frontmatter, placeholders, repo scan, import mapping

**Status: implemented (2026-08-28).** Amends [D75](D75-path-portability-across-machines-placeholders-auto.md) and [D74](D74-import-of-external-non-okf-wikis-kbs-kb-import-skill.md).
Closes #183.

**Context.** Four independent defects, grouped because none justified a decision of its own and each
one blocked **writing something down** or **getting a corpus in**.

1. **A multi-line flow list was rejected.** The parser looked only at the current line, so
   `provenance: [\n  a,\n  b,\n]` — valid YAML, and what any editor produces for a long list — failed
   with `unclosed flow list`. 63 files in one corpus used the form and one of them aborted an import.
   The block-list branch already had the lookahead; the flow branch simply did not use it.
2. **The placeholder syntax could not be written down.** `{{repo:<name>}}` in a skill documenting the
   generic form was indistinguishable from a real reference, producing eleven warnings per sync — which
   trains people to ignore warnings — and the only workaround was to describe the syntax in prose,
   without braces, in the one place where showing it verbatim is the point.
3. **The repo scan stopped at a fixed depth of 4.** A workspace organised as
   `<root>/<program>/<area>/<repo>` put clones one level too deep: ~160 repositories were invisible and
   every `{{repo:<key>}}` citing one was unusable. The failure named no depth, so the limit had to be
   guessed.
4. **`--map` needed one flag per exact source directory.** 58 directories, 58 flags, because the lookup
   was keyed on `path.Dir(rel)` with no prefix semantics. `--default-map` covers *everything*, so the
   only choices were one map for the whole corpus or one flag per directory — with nothing in between,
   which is exactly where a real corpus lives.

**Decision.**

- **Flow lists span lines**, terminated by the first depth-0 `]`, with bracket characters inside quoted
  scalars skipped. A **trailing comma yields no empty element**: it is valid YAML and what every
  wrapping editor emits. An unclosed list stays an error and gains **the line number** of the key.
  Scanning stops at the frontmatter terminator: a `[` with no `]` before `---` is unclosed, not a
  licence to consume the body.
- **The file name comes from the callers, and needed no new code.** `Validate` already wraps the parser
  error with the concept's path, and `applyImportPlan` already reports the source path and **counts the
  file as an error without aborting** the rest of the batch. Together with the line number from the
  parser, the report's ask — file, key, line — is satisfied. **A lint `frontmatter_malformed` check was
  therefore dropped**: `validate` covers it, and adding a duplicate would only double-report. This is
  the plan's own "defer to it if it exists" branch, taken.
- **An explicit escape *and* a metasyntax heuristic, both.** The escape `{{\repo:...}}` is
  authoritative and removes the backslash; chosen over doubling the braces (unreadable in a document
  about the syntax) and over an HTML-comment wrapper (skills are also read as plain markdown). The
  heuristic silences **the warning only** for a key of the form `<name>` or `...`, leaving the text
  verbatim — which is what a documentation example wants — and fixes every existing document with no
  edit. A real key cannot look like metasyntax: `repoindex` keys are git remote names or path keys, and
  `<` `>` are legal in neither, so no lookup is needed to be sure.
- **Scan depth is configurable per client, not per root**: per-root depth would turn `search_roots`
  into a list of objects, a config-format break for a case nobody asked for. Default stays **4** —
  raising it for everyone would slow every resolution to accommodate one layout — and the maximum is
  **8**, because the scan runs on every unresolved placeholder and an unbounded depth on a large home
  directory is a multi-second stall mid-sync. A value above the maximum is **clamped**, not rejected:
  a config value that stops a sync is worse than one adjusted loudly.
- **`--map` is longest-prefix, matched at segment boundaries** so `a/b` never covers `a/bc`, with exact
  match as the degenerate case and `.` as a legal catch-all. **The matched prefix is replaced, not
  appended to**: the destination is a map, and the write path caps concept depth at three segments, so
  mirroring an arbitrarily deep source tree cannot work — preserving hierarchy is `--dir-as-concept`'s
  job. A duplicate source is an error rather than the later flag silently winning, and a `--map` that
  matches nothing warns, because otherwise a typo falls through to `--default-map` unnoticed.
  `--dry-run` now names the flag behind every destination.
- **The portability inputs became a struct.** `materializeForProviders` already took `searchRoots` and
  `paths` positionally; a third positional argument in a seven-argument call is how the next bug gets
  written.

**Consequences.** Mostly additive: frontmatter that previously failed to parse now parses, one new
optional client field with the default unchanged, new escape syntax nothing currently uses, and fewer
warnings with no edit. One **behaviour change**: a `--map` that previously matched one directory now
matches its subtree, so an import relying on subdirectories falling through to `--default-map` routes
them differently — `--dry-run` is the check, and it now attributes every destination to its flag. One
existing contract test moved its "empty entry" case from a trailing comma to an interior one, since the
former is now valid YAML.
