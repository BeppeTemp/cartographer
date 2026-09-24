---
topic: concurrency-git
---

# D265 — The author is the committer when git has none

**Decision.** When Cartographer passes `--author` to a commit and git cannot
resolve a committer (`git var GIT_COMMITTER_IDENT` fails and the per-KB env names
none), `gitx` sets `GIT_COMMITTER_NAME`/`GIT_COMMITTER_EMAIL` to that author.
`kb.Init` returns the initial commit's error (only "nothing to commit" is benign),
so `kb create` and `setup` stop there instead of pushing an unborn branch.
Closes #410.

**Why.** `--author` sets only the author. On a machine with no `user.email` (a
fresh Git for Windows install is the common case) git exits 128 with `Committer
identity unknown` on every commit: the server's `concept_write` never committed,
and `kb create` swallowed the error, then failed the push with `src refspec main
does not match any` under a hint that blamed authentication. The probe costs one
extra `git var` per commit that carries an author but no committer env.

**Alternatives rejected.**

- Always set the committer to the author whenever an author is supplied: simpler
  and no probe, but it silently replaces a committer the operator did configure
  in git (`user.name`/`user.email`, `GIT_COMMITTER_*` in the process), which is
  what the history records today on every machine that works.
- Refuse to commit and tell the user to configure git: correct but the server has
  no user to tell at commit time, and the author Cartographer already resolved is
  a truthful identity for the commit.

**Consequences.** A commit made through `gitx` with an author never fails for a
missing committer. A per-KB committer (D46) still wins: the probe is skipped when
env carries both committer variables. A failed initial commit now leaves a scaffold
with no commit behind an error that names it; the unborn-HEAD case on an existing
scaffold is handled separately (#416).
