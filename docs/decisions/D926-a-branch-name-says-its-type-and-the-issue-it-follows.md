---
topic: project-governance
---

# D926 — A branch name says its type and the issue it follows

**Decision.** A branch is `<type>/<issue>-<slug>`, with `<type>` one of `feat`,
`fix` and `hot-fix`, and its worktree is `.worktrees/<issue>-<slug>`.
`make worktree-add ISSUE=<n> SLUG=<slug> [TYPE=…]` creates both and refuses any
other type or an issue that is not a number; `make worktree-rm` deletes the
branch its worktree is on. Only a live session leaves the issue out: it has none
until its draft PR.

**Why.** Every branch used to be `feat/<slug>`: a bug fix lived on a `feat/`
branch, and nothing tied a branch or a worktree to the issue it implements, so
telling which local branches belong to merged, open or abandoned work meant
opening each PR. With several plans in flight, one worktree each
(`implement-issue`), the issue number is the key that joins `git worktree list`,
the issue and its decision file, which D672 numbers after the same issue. The
cost: one more argument to `make worktree-add`, and a branch type coarser than
the conventional-commit type of the PR title.

**Alternatives rejected.**
- The conventional-commit types as branch types (`docs/`, `chore/`,
  `refactor/`…): the PR title already carries the exact type and the `pr-title`
  check lints it; an unchecked second copy in the branch name would drift. The
  branch only says whether it adds something, fixes it, or fixes a release in a
  hurry.
- The issue number inside `SLUG` by convention (`SLUG=123-my-change`): nothing
  would check it, and `worktree-rm` would still guess the `feat/` prefix.
- `ISSUE` always required: a live session has no issue until its draft PR, and
  the next number cannot be guessed (D703).
- Renaming the existing branches: churn for open PRs and every clone, for no
  gain. The rule applies to new branches; `worktree-rm` still removes the old
  ones.

**Consequences.** The skills and the `plan-implementer` agents push `HEAD`
instead of rebuilding a branch name from the slug, so a new type changes only
the `Makefile`. `worktree-rm` reads the branch from the worktree and deletes it
only when its type is one `worktree-add` creates; a branch checked out there by
hand stays. The branch type does not choose the PR title: a `hot-fix/` branch
lands a `fix:` PR, a documentation change rides a `feat/` branch with a `docs:`
title.
