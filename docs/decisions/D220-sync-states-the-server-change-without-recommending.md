---
topic: client-configurator
---

# D220 — `sync` states the server change; only `status` and `doctor` recommend `reconnect`

**Decision.** The D142 server-change line has two wordings, one detection.
`status` and `doctor` keep theirs verbatim — the fact plus "run `cartographer
reconnect` to rebuild the client configuration". `sync` prints its own: the
fact, that this run re-applies the current manifest, and that `reconnect` is
needed only for files an older version wrote under names no longer in the
managed set. No imperative. The line keeps its position, before the sync
output.

**Why.** `sync` printed the `status`/`doctor` sentence *ahead* of doing the
work that makes it moot. Read in order, an unhedged imperative on the first
line is a prerequisite: observed on a client upgraded from a v0.12.2-era state
to a v0.13.0 server, where the notice printed, the sync completed, every
provider came back `in-sync`, and `reconnect` — a full disconnect + connect —
was never needed. The cost is that one fact now has two sentences that can
drift apart. It buys the reader, and above all the agent that runs
`cartographer sync` at every session start through the bootstrap hook, a line
it can act on correctly without having run the command first.

**Alternatives rejected.**
- *Move the line after the sync result.* When a sync fails, the fact that the
  server changed is the most useful thing on screen, and it would be buried
  under the failure or lost with it.
- *Change the one sentence for all three commands.* `status` and `doctor` only
  observe; naming the repairing command is the single actionable thing they can
  offer (D143). Softening their wording would remove the remedy from the two
  places that exist to point at it.
- *Drop the line from `sync` entirely.* D142's invariant is that the change is
  reported; a silent reconfiguration against a different server is the failure
  mode that decision exists to prevent.

**Consequences.** The detection — the version comparability rule, the lockfile
read, the per-provider dedup, one line per invocation — stays in a single
function, and both wordings are formatting on top of it: an edge case fixed
once is fixed for both. A future change here must keep the two sentences
distinct; the test asserting the `sync` wording contains no "run `cartographer
reconnect`" is what stops them collapsing back. No flag, config or protocol
change: user-visible text only.
