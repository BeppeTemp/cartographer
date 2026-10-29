---
topic: data-plane
---

# D1377 — Junk is excluded locally, and a tracked one is untracked by kb_repair

**Decision.** `kb.Init` and `kb.Open` write `kb.JunkPatterns` into `.git/info/exclude`, so no server commit stages a junk file anywhere in the tree, the KB root included. `junk_file` carries an `untrack` fix that `kb_repair` applies with `git rm --cached` (working-tree file kept, one commit); it is not auto-repair-safe. `artifact_move` treats junk as no part of an artifact: it is never carried and never a reason to refuse.

**Why.** `data/.gitignore` (D316) covers `data/` only and D62 removed the root one, so a Finder `.DS_Store` at the KB root went into the external-changes commit (D357) and then into a finding no tool could close. `.git/info/exclude` is local and never versioned, which D62 already prefers for KB-local state, and `Open` makes every existing KB self-migrate. The cost is that an excluded file is also an *ignored* file, which the artifact-move guards treat as unrecoverable; they now skip junk.

**Alternatives rejected.** A root `.gitignore`: D62 removed it, and it would travel with the KB. Deleting the file in the repair: the working-tree copy is the OS's, not ours. Auto-repairing it: kept manual until there is evidence it is wanted unattended.

**Consequences.** Exclusion never untracks a path already committed, hence the repair. The fix passes names to git literally (`--literal-pathspecs`): a tracked `x/*.pyc` is one file. A KB whose `.git` is a pointer file (a git worktree) gets no local exclude, as for the other `ensureInfoExclude` entries, so `git add -A` can still stage junk there. A change to `JunkPatterns` reaches existing KBs at their next `Open`, but never removes an entry already written.
