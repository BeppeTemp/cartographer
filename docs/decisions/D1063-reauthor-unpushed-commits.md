---
topic: concurrency-git
---

# D1063 — An identity refusal is its own error, and unpushed commits are re-authored by an offline operator command

**Decision.** A push the remote refuses because of the commits' author or committer is reported as an
identity refusal (`*kb.ErrIdentityRejected`), tested before the protected-branch wording, and never tells
the operator to unprotect a branch. Commits that exist only in the local clone are re-authored, under the
configured `git.identity`, by the offline command `cartographer kb reauthor <kb> [--apply]`, which runs under
the KB's process lock, moves the branch with a compare-and-swap `update-ref`, and refuses anything already
reachable from a remote ref. Messages are kept, so the `Cartographer-Author`, `-Token` and `-Host` trailers
stay as the record of who really wrote.

**Why.** The generic `pre-receive hook declined` line accompanies both a branch protection and a membership
rule, so the old classification sent operators to fix the wrong thing. The automatic realign (D950)
deliberately leaves a token-authored commit alone, and such a commit can never publish under a membership
rule: nothing repaired it, and the only way out was git by hand in the clone, which agents are forbidden to
run. The cost is a history rewrite, accepted only on commits no one else can have seen, behind a preview.

**Alternatives rejected.**
- A flag on `kb repair`: that command is a thin MCP client for mechanical lint repairs, and has neither the
  server config nor the clone.
- A new MCP tool: a history rewrite is not a write (a handler never locks, commits or syncs), the identity is
  a server-file setting, and any `rw` token would gain the power to rewrite history.
- Making the automatic realign re-author token-authored commits too: a token id names the real writer, and
  silently replacing it on every sync loses that without an operator deciding to.
- Stopping the service for the rewrite: the process lock and the compare-and-swap make a race fail safely.
- An audit-trail entry: no offline command writes one; the reflog, stderr and this file record it.

**Consequences.** `last_error` text is the contract (no new field in `sync_status`). `RealignCommitters`
keeps its decisions byte for byte and shares its commit loop with `ReauthorCommits`. A range holding a merge
is refused by both; the operator resolves that on the remote side. Any new push-refusal wording belongs in
`gitx.IsIdentityRejection`, and the identity test must stay ahead of the protected one.
