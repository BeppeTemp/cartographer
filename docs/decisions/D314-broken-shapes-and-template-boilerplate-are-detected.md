---
topic: data-plane
---

# D314 — Broken shapes and template boilerplate are detected

**Decision.** Lint gains `stringified_list` (a list field stored as a string such as `"[a, b]"`, repairable by `kb_repair`), `mangled_placeholder` (a `{{repo:…}}` unwrapped into prose by an import) and `missing_registry` (placeholders cited, no `paths.yaml`). `repeated_fact` exempts every line a template carries, not only blockquotes, and quotes the original line, code spans included.

**Why.** Doctor sessions on real KBs found shapes no check saw: the parser sends a quoted `"[a, b]"` to the scalar branch, so the value is a string and nothing looks wrong; D263 is opt-in by the presence of `paths.yaml`, so a KB that never wrote it got no signal. `repeated_fact` was noisy on template table rows and illegible where a code span had been masked to spaces.

**Alternatives rejected.**
- Fix the parser to read `"[a, b]"` as a list: it would change the meaning of a legitimately quoted string and hide the damage instead of reporting it.
- Make `unknown_placeholder` fire without a registry: every cited key is then unknown, which floods; one KB-level `info` nudge says the same once.
- A secondary `mangled_placeholder` pattern on any `` `repo:x` `` code span: too many false positives on pages that document the syntax; only the prose-suffix form is matched.
- Use `MaskCodeSpans` before scanning for the mangled form: it blanks the very code span the pattern is anchored on, so fenced blocks are skipped by a local scan instead.

**Consequences.** `stringified_list` is not suppressible (like `malformed_frontmatter`); `mangled_placeholder` is, since a page documenting the syntax may use it. `missing_registry` is dismissed by creating `paths.yaml`, not by `lint_ignore` (a KB root has no frontmatter). `factLines` returns comparison key to original line: the key stays the masked form, so matching is unchanged. A new `kb_repair` fix kind, `listify_field`, parses the string with `lint.ListItems`.
