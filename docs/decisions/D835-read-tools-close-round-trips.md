---
topic: control-plane
---

# D835 — The read tools answer three follow-ups in the first call

**Decision.** Three additive changes to the read tools, each removing a second
call an agent used to need:

- `concept_list` accepts `content_hash` in `fields` as a pseudo-field: not a
  frontmatter key but the concept's hash, the same `okf.ContentHash` that
  `concept_read` returns and `if_match` expects. It does not count toward the
  cap on `fields`.
- `concept_read` accepts `match` together with `section`: the grep runs inside
  that one section, `line` and `start` count from the section's first line as a
  plain `section` read returns it, and the response echoes `section`.
- The `outline`, `section`, size-guard and `match` responses of `concept_read`
  carry the parsed `frontmatter`, as the full response already did.

**Why.** Issue #835: an agent checking which concepts changed listed them and then
read each one for its hash; one grepping a large page read the whole body to
search one part of it; one reading a field of a page over the 60 KB guard got an
outline and had to force a full read for the frontmatter.

**Alternatives rejected.** A separate `concept_hashes` tool: one more entry in
the `tools/list` budget (D285) for what one value in `fields` expresses. Keeping
`match` exclusive with `section` and grepping client-side: it is the full read
the change avoids.

**Consequences.** All three are additive: a caller that does not ask gets the
same response as before. `match` stays exclusive with `outline` and
`with_content`. The detail lives in `docs/control-plane.md` §API MCP, not in the
tool descriptions, which the budget keeps short.
