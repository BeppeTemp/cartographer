---
topic: control-plane
---

# D324 — lint filters by check and paginates

**Decision.** `lint` takes `check` (a name or a list), `limit` and `offset`, and answers
with `findings_in_page`, `findings_after_filter`, `offset` and, when more follow,
`next_offset` and `truncated`. A page holds at most `lintMaxPage` = 200 findings.
`lint.Filter` sorts what it keeps by severity descending, then check, path and message
ascending, so an offset names the same finding on every call.

**Why.** On a KB of ~760 concepts `lint` returned 2361 findings in one payload; an agent
working one check at a time had to download all of it to filter locally. `kb_review` and
`work_list` already page; `lint` was the one governance tool with no facet. Findings came
out in map-iteration order, which makes an offset meaningless. The cap is a judgment about
a caller's context (~150 bytes a finding, ~30 KiB a page), not a protocol limit.

**Alternatives rejected.**
- Filter and page inside `lint.Run`: it feeds the cache (D294), `gate_check` and the Atlas
  endpoint, which must all see the whole set.
- Sort in `lint.Run`: the cache would store a sorted slice for every caller; sorting in
  `Filter` costs one sort per call and gives the Atlas endpoint the order for free.
- Reject an unknown `check`: the filter is a view, an empty page already says "none".
- Page `gate_check` too: it is a verdict, not a browser.

**Consequences.** `check` is a view filter exactly like `severity_min`: `count`,
`counts_by_check` and `counts_by_severity` are computed before it, and `findings_omitted`
keeps meaning "hidden by `severity_min`". Without `limit` a call returns the first 200
findings and `next_offset`, not the whole list: a caller that needs all pages by following
`next_offset`. Callers that relied on the unsorted order break (none known).

The D285 budget counts the whole `tools/list` payload, input schemas included, and the
agent profile sits within a few bytes of it: the three new parameters were paid for by
shortening the `lint` description, whose detail lives in `docs/control-plane.md`.
