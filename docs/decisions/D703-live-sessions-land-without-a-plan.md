---
topic: project-governance
---

# D703 — A change found by trying it lands from a live session, without a plan issue

**Decision.** There are two ways to land a change. A change that can be specified
before it is built goes through a plan issue (`plan-issue` → `implement-issue`).
A change whose outcome is found by trying it is made in a **live session**: one
session talks with the user, runs `make dev` from its worktree (the server
rebuilt on every Go change, the UI hot-reloaded, a copy of the data with its
remotes removed), and delegates bounded edits to a `live-implementer` subagent
on a cheaper model, a thin per-client wrapper over the `live-dev` skill like
`plan-implementer` (D327). The session opens its PR as a draft after the first
accepted step, so the decision number is the PR number (D672) from that moment,
and it ends in one PR whose title and body are the record that lands in the
release changelog.

**Why.** For the Atlas UI and for anything tuned by eye, the user decides by
seeing the result: a plan written before that point either guesses or is
rewritten after every try, and the issue-to-subagent handoff adds a round trip
to every iteration. Without a plan issue, nothing else records what was done,
so the PR has to: its title and body become the squashed commit and the
changelog entry. What this costs is the second reader of the design: the user
judges the behaviour, so an independent read of the diff before the merge is
required, as in `implement-issue` §3.

**Alternatives rejected.**

- A plan issue for every change, written after the live work: a record written
  to satisfy a rule, describing what is already merged.
- No subagents in a live session: every edit then passes through the
  coordinator's context and the most expensive model, including the closing
  docs and tests.
- Deriving the decision number from the highest issue or PR number: two sessions
  working at once take the same number (it happened while this was written).
- A file watcher (air, watchexec, entr) for `make dev`: a dependency for a
  one-second `find -newer` poll.
- Serving the real data directory: a live session writes to it, and git sync
  would push those writes.

**Consequences.** Every live-session PR carries a `feat:` or `fix:` title when a
user would notice the change, and one body line per further user-visible change.
`live-implementer` never commits: git, the instance and the decision stay with
the coordinator. Two live sessions on one machine need distinct `API_PORT` and
`UI_PORT`. The three `live-implementer` definitions must say the same thing.
