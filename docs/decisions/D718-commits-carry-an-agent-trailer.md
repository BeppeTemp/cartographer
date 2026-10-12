---
topic: concurrency-git
---

# D718 — Commits carry an `Agent:` trailer naming the calling MCP client

**Decision.** `gitWrap` appends `Agent: <clientInfo name>` to the commit of every
agent-driven write, in one trailer paragraph after `Reason:` and before the
`Accepted-Finding:` lines. `changes_since` returns the distinct agents per
concept (`agents`) and, for a caller who sees the whole KB, `commits_by_agent`;
`concept_history` returns `agent` per revision.

**Why.** Several agent clients of one user share one git author (and one token),
so history could not say which client wrote what; the per-token author (#594)
only separates agents that hold a token each. The MCP `clientInfo` name is
already on the request context (the doctor nudge reads it), so the trailer costs
no new protocol surface. Only the name is stored: the client is the grouping
key, a version per commit is noise.

**Alternatives rejected.**
- Storing name and version: noise in every commit and a key that splits on every
  client upgrade.
- An `unknown` placeholder when the client did not identify itself: absence is
  honest and keeps old and new commits alike.
- A new `changes_since` parameter to group by agent: the additive fields cost
  nothing to callers that ignore them.
- The audit log or `log.md`: the first is a leakage allow-list, the second the
  curated narrative (same reasoning as D272).

**Consequences.** The name is self-declared by the client: the trailer is an
attribution hint, never an identity; the authenticated identity stays the git
author. The name is client-controlled text going into a commit message, so
whitespace (newlines included) is collapsed and the value cut at 64 bytes on a
rune boundary; without that a name could forge a `Reason:` or `Accepted-Finding:`
trailer. Server-originated commits (`external changes`, auto-repair) carry no
`Agent:`. In `gitx` the `Reason` field must stay last in the `--pretty` formats.
`commits_by_agent` is filled only under `WholeVisible`, like `commit_count`.
Builds on D272, D350 and #594.
