---
topic: data-plane
---

# D1558 — The frontmatter subset: validated keys, block scalars, inline comments

**Decision.** The OKF frontmatter parser reads the YAML subset the way YAML does
where hand-written and imported pages need it: an indented line outside an open
block is never a key; block scalars (`|`, `>` with chomping and indentation
indicators) read as their logical string and are kept verbatim until the key is
overwritten; an unquoted scalar ends at a `#` preceded by whitespace and a
quoted one at its closing quote, the comment being kept on the entry and written
back; a leading BOM is ignored. The write boundary refuses keys the serializer
cannot write back (`okf.CheckKey`: empty, `:`, `#`, line breaks, edge
whitespace, `- ` prefix). Amends D309: the serializer emits only what the parser
rereads, now for keys as well as values.

**Why.** The serializer regenerates every key line on any write, so whatever
the parser misread was baked into the file by the first unrelated
`concept_patch`: continuation lines became column-0 keys, comments became data.

**Alternatives rejected.** Refusing block scalars with `malformed_frontmatter`:
simpler, but writes would fail on exactly the imported pages D162 targets.
Dropping inline comments on rewrite: loses text. Keeping the lenient ` #` reading:
the value differs between this parser and every YAML reader.
Strengthening `VerifyRoundTrip` to compare keys and values: would refuse writes
that succeed today and needs the original frontmatter passed in.

**Consequences.** A hand-written plain scalar such as `title: Fix bug #12` now
reads `Fix bug` (the write path already quoted any value with `#`). A comment on
a key holding a nested block is not preserved. Edge Unicode whitespace is always
quoted.
