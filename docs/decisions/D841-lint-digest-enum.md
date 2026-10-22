---
topic: control-plane
---

# D841 — `lint` folds its output with one `digest` enum, not three parameters

**Decision.** The `lint` tool gains one optional parameter, `digest`, an enum of
`by_map` (adds `counts_by_check_and_map`), `paths` (one `check`, answers its
distinct sorted paths) and `compact` (each finding as `{check, path}` only). A
`limit` above the page cap is reported with `limit_clamped: true`. The default
output is unchanged; the modes are read-only and add no lock or commit.

**Why.** Issue #841 proposed three separate parameters (`group_by`, `distinct`,
a compact flag). The agent-profile `tools/list` budget (D285) is shared by every
tool, and each top-level parameter costs bytes there for every session whether it
uses the feature or not. One enum costs one key and its values; a further mode is a
new value, not a new key.

**Alternatives rejected.** Three parameters: more schema bytes and combinations
that must be refused (`distinct` without a `check`). `omitempty` on the shared
finding type for the compact form: it would change the full-output contract
(message always present, D342); `compact` uses its own type.

**Consequences.** `by_map` counts are built on the unfiltered findings, like
`counts_by_check`, so the rows add up to it whatever `check` says. A mode that
returns a different shape (`paths`) is a mode, not a flag: a new digest value must
say what it answers in `docs/control-plane.md` §API MCP, and detail stays there,
not in the tool description.
