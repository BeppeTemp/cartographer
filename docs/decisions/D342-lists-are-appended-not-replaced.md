---
topic: control-plane
---

# D342 — Lists are appended and removed server side; a silent shrink is flagged

**Decision.** `concept_patch` and the `patch` operation of `concept_batch` take
`frontmatter_append` and `frontmatter_remove`, computed on the list read from disk
inside the write path. A `frontmatter` merge that drops items from a list answers a
write-time `list_items_dropped` warning. `concept_read` with `with_content` returns
`content` instead of `body`, gains `match`/`context`, and `concept_patch`/`concept_write`/
`concept_new` always answer `findings` (an array), `edit_matches` for an `edits` call,
and an `old_string_not_found` hint with the closest line.

**Why.** Four agents each added one `provenance` entry; one resent a list rebuilt from
a stale copy and lost what the others had added. `if_match` could not catch it: the
hash was current, only the list content was stale. Only an operation computed against
the value on disk is immune to a stale copy.

**Alternatives rejected.** A stricter `if_match`: the hash was right, so nothing to
tighten. Refusing a shrinking merge: a legitimate curation of a list must stay possible,
and D312 findings never fail a write that already succeeded. A `body: false` flag on
`concept_read`: a new parameter against a 600-character, 25 KiB description budget.
A lint check for the dropped items: lint cannot know the previous state.

**Consequences.** `concept_write` is deliberately outside the `list_items_dropped`
guard: it replaces the whole page by contract and its caller states the full list on
purpose. `list_items_dropped` is write-time only (no `conformanceChecks` entry, no fix,
not suppressible by `lint_ignore`). Clients that tested for an absent `findings` key or
read `body` next to `content` must adapt. `frontmatter_append`/`frontmatter_remove` are
in `lint.ToolParamFields`, so they cannot be frontmatter keys.
