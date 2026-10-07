---
topic: concurrency-git
---

# D335 — KB writes follow a configured branch

**Decision.** A local-profile KB may name its branch with `kbs[].git_branch`.
When it is set, that branch is canonical instead of the remote's default branch:
it is the only branch pulled and pushed, the first push creates it on the remote
when it is missing, and a clone on any other branch is refused writes with
`ErrBranchDiverged`, as in D264. When it is unset, D264 applies unchanged. A
fresh clone is checked out on the configured branch; an existing clone is never
switched.

**Why.** Users asked to keep a KB on a branch of their choosing, for example a
repository whose default branch belongs to something else. D264 rejected a
`branch:` key as a second source of truth, but the damage it guards against is an
*undeclared* branch forking the KB without anyone noticing. A branch the operator
declares in the server config is intent, not drift, so creating it is the expected
outcome. The cost: the configured branch and the forge's default can now differ
on purpose, and someone looking only at the forge sees the KB on a non-default
branch.

**Alternatives rejected.**
- Refusing to push a configured branch the remote does not have, so the operator
  creates it first: one more manual step for no added safety, since the
  declaration already says which branch to create.
- Checking out the configured branch in an existing clone at startup: it could
  strand unpushed commits on the old branch. Refusing with a recovery message is
  reversible; a silent switch is not (the same reasoning as D264).
- A global `git.branch`: KBs on one server live in different repositories, and
  one branch name for all of them is not a real need.
- Reusing the server profile's `git_base_branch`: that profile pushes a working
  branch and merges through a PR, which is a different workflow.

**Consequences.** This amends D264 only in its rejected `branch:` alternative.
Everything else in D264 (never creating an undeclared branch, never repairing a
divergence) still holds. `sync_status` reports `branch_source` (`config` or
`remote`) so an operator can tell which rule applies. `git_branch` together with
the server profile fails at startup. `cartographer kb clone` (client side) still
clones the remote's default branch.
