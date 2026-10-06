---
topic: data-plane
---

# D309 — Writes never corrupt frontmatter or drop graph edges

**Decision.** The OKF serialiser only emits what its own parser reads back,
and the write path verifies it: a scalar holding any flow-list delimiter or
quote (`'` included) is quoted; overwriting or removing a key also drops the
indented lines its old value left behind; blank lines before the first key are
dropped; and `prepareWriteConcept` parses the serialised frontmatter and
rejects the write if it does not parse. `reciprocal_link_item` counts a
back-link only when it sits in the target's text, not in the target's own
links section, and `kb_repair` never drops both items of a mutual pair in one
run. `concept_patch`/`concept_batch` accept an `unset` array of keys to
remove, and `concept_read` returns the parsed `frontmatter` next to
`frontmatter_raw`.

**Why.** Each of these left a production KB in a state no tool could repair
cleanly. An unquoted `dall'operatore` in a flow list opened a single-quoted
string that never closed, so the file stopped parsing; a `provenance: text`
followed by `  - item` lines kept the items as unrecognised lines that
`Serialize` printed after the new value; two pages listing each other only in
their links sections were both flagged, and repairing both findings removed
the edge from the graph — the one thing D301 promised backlinks would keep.
Some MCP clients strip `null` from the arguments, so the D88 removal path was
unreachable from them; an agent re-parsing `frontmatter_raw` by hand
introduced its own quoting errors. The cost: values with an apostrophe are now
quoted, so files show a one-time diff on their next write, and every write
pays one extra frontmatter parse.

**Alternatives rejected.**
- Fixing the parser to accept unquoted `'` inside a flow element: the
  parser's quote rules are what lets `]` and `,` live inside an element; the
  serialiser is the side that must adapt to them.
- Pruning orphaned continuation lines at parse time: it would silently
  rewrite files a read never meant to change; doing it in `Set`/`Delete`
  limits it to the key the caller is replacing.
- Removing every blank line after the key, as first planned: that also
  collapsed blank lines a person used to group keys; only the run up to the
  last indented line goes.
- Keeping only the repair-side guard for mutual pairs: the lint would still
  report findings no repair applies; the lint fix is primary, the guard is
  defence-in-depth.
- Replacing `null` with `unset`: `null` is the documented contract (D88) and
  works from most clients; `unset` is an alternative, not a replacement.

**Consequences.** `serializeScalar`'s character set and the quote handling of
`splitFlowList`/`collectFlowList` are one contract: a change to either needs
the round-trip tests in `internal/okf` to stay green (a `\"` inside a quoted
element did not round-trip until `collectFlowList` learned the escape). The
round-trip guard turns any future serialisation regression into a refused
write instead of a corrupt file. `unset` is a tool parameter, so a frontmatter
key named `unset` is now a `tool_param_field`. A bundled `kb-doctor` note
tells agents on an older server to dry-run `reciprocal_link_item` first.
