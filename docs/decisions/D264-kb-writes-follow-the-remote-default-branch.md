---
topic: concurrency-git
---

# D264 — KB writes follow the remote default branch

**Decision.** In the local Git profile, a KB with a remote and `git.sync` on
writes only to its canonical branch: the remote's default branch, asked of the
remote on every sync (`git ls-remote --symref`). A clone checked out on any
other branch is refused writes with `ErrBranchDiverged`, state `degraded`, an
error naming both branches and the recovery; reads keep working. `SyncOut`
never creates a branch on the remote, except the first push of `main` to an
empty remote. A clone of an empty remote is pinned to `main` and given its
initial commit, like `kb create`. Agents are told, in the generated
instructions block, never to run git in a KB's clone.

**Why.** A team's new shared KB ended up with its history split across two
branches of the same remote. The local profile had no notion of "the KB's
branch": `SyncIn` pulled and `SyncOut` pushed whatever was checked out, and
`git push origin <branch>` silently creates a missing branch. Any path that left
a clone on a non-canonical branch — the host's `init.defaultBranch` on the clone
of an empty remote, or an agent "fixing" a stuck sync with raw git — forked the
KB on the remote without a warning and with status `clean`. The cost is one
extra `ls-remote` round trip per sync (bounded by the fetch timeout), and a KB
already sitting on a non-default branch stops accepting writes after upgrade
until its operator reconciles it.

**Alternatives rejected.**
- A `branch:` configuration key: a second source of truth that can itself
  diverge from what every clone gets and what the forge shows.
- Reading `refs/remotes/origin/HEAD`: `git clone` writes it once and `git fetch`
  never refreshes it, and a repository that was initialised and then given a
  remote has none. `git remote set-head --auto` fixes staleness but still
  cannot tell an empty remote from a network error; `ls-remote --symref`
  answers default branch, branch list and emptiness in one call, and the local
  `origin/HEAD` is re-pointed from its answer.
- Repairing divergence automatically (merge, rebase, checkout of the default
  branch): merging two histories is an operator decision, and a checkout by the
  server could strand an agent's or an operator's work. Refusing is reversible;
  a wrong merge pushed to a shared remote is not.
- Pushing the checked-out branch and warning: the fork on the remote is exactly
  the damage to prevent, and a warning in a log is what went unnoticed.

**Consequences.** When the remote's `HEAD` names a branch it does not have (a
self-hosted bare repository initialised with `master`, then pushed `main`),
`main` is canonical if the remote has it; otherwise the canonical branch is
unknown, the `SyncIn` comparison is skipped, and `SyncOut` still refuses to
create a branch. The canonical branch last resolved is cached on the KB so
`SyncOut` can refuse without a network call when `SyncIn` was skipped by the
freshness window. The server profile (D117) keeps its own validated base and
working branches and is not subject to this check. A KB without a remote or
with `git.sync` off behaves as before. Current behaviour: `docs/concurrency.md`
§Git profiles and §Remote synchronization.
