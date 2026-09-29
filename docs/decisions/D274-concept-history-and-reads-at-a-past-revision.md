---
topic: concurrency-git
---

# D274 — Per-concept history is a read-only tool, and `concept_read` takes a commit `rev`

**Decision.** `concept_history(id, [limit])` lists the commits that touched a
concept, following renames, with the `Reason:` trailer of D272. `concept_read`
gains an optional `rev`, a 7–40 hex commit SHA, and returns the concept as of
that commit. Both are read-only, scoped and hidden exactly like `concept_read`,
and `concept_history` is in the core agent profile.

**Why.** Every write is a git commit, but a remote client has no clone and an
agent is told not to run git in the KB, so "when and why did this page change"
and "what did it say before" were unanswerable over MCP; `changes_since` only
aggregates by time window. The history is `git log --follow` on the concept's
current file (`gitx.FileHistory`), the same record format as `LogNameStatus`.
`rev` resolves through that history: the newest entry that is `rev` or an
ancestor (`gitx.IsAncestor`) is the version in force at `rev`, which is what
makes a read across a rename or an expansion work.

**Alternatives rejected.**
- A server-side diff tool: adds a format to maintain; an agent compares two
  `concept_read` results itself.
- Accepting any git ref for `rev`: the value reaches a git argument, so only a
  hex SHA is allowed (no branch names, `HEAD~n`, or option-like input).
- Advanced profile, like `graph_path`: reviewing why a page changed is a normal
  step before editing it.

**Consequences.** `--follow` is a heuristic: when git's rename detection misses
a rename or an expansion the history starts there, and a new file very similar
to an existing one can inherit that file's history. The history walk for `rev`
is capped at 1000 commits. `content_hash` of a historical read is not an
`if_match`. Without git, or with auto-commit off, `concept_history` returns an
empty list and a note rather than an error.
