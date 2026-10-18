---
topic: concurrency-git
---

# D903 — one way to write, any provider

**Decision.** Cartographer writes to a KB's git the same way everywhere, with no
per-install git profile and no provider coupling. The server git profile — a
protected base branch, a working branch, a GitHub forge, and the `pr_status` /
`pr_finalize` tools — is removed (this supersedes D117). A KB that is a git
repository always commits after a write, and syncs exactly when it has a remote:
`git.autocommit` and `git.sync` are gone. `git.in_window` and `git.out_debounce`
become the constants `SyncInWindow` (30s) and `SyncOutDebounce` (3s). The branch
a KB writes to is `branch:` in its own `settings.yaml`, resolved from the copy
committed on the remote's default branch, replacing `kbs[].git_branch`. Every
removed key is recorded in a per-file removed-key registry, and an upgrade
realigns a settings file into its canonical form through the shared
`settingschema` machinery (WP1), warning on a key edited back in by hand.

**Why.** The settings had grown to ~140 keys, many of them switches that turned a
feature off, so long-lived installs drifted into near-random configuration. The
git profile in particular fought the agent's write loop — a PR per write — and
tied the product to one forge, when Cartographer's own gates already review
every write: the lint gate, `commit_gate`, a revertible commit per write, the
audit trail, token roles and signed artifacts. The accepted cost is the loss of
a mandatory human review before merge; that guarantee now lives in branch
protection on the remote, reported as `ErrProtectedBranch` rather than merged by
Cartographer.

**Alternatives rejected.**
- *Keep the profile as an opt-in, default off.* Rejected: an off-by-default
  feature is exactly the drift P4 targets, and it keeps the forge coupling alive.
- *A `git.provider` key to abstract the forge.* Rejected (P3): the gates make a
  forge unnecessary, so the honest move is no provider, not a pluggable one.
- *Keep `git.autocommit` / `git.sync` as switches.* Rejected: "a git repo
  commits and syncs-when-it-has-a-remote" is an invariant, not a preference; a
  switch only lets an install break it silently.
- *A key for the Cloud Run synchronous push (the old `out_debounce: 0`).*
  Rejected: a platform that freezes idle instances is a fact about the runtime,
  detected from `K_SERVICE`, not a choice an operator should have to make.

**Consequences.** A KB still carrying `git_profile: server` refuses to mount with
a message naming the way out; `pr_status` / `pr_finalize` are gone from the MCP
surface (public-contract break, called out in the release notes). The branch
resolution reads `settings.yaml` from the remote's default branch, so a `branch:`
change has no effect until it reaches the default branch — a future change to the
sync layer must preserve that, or a KB can fork on the remote. The removed-key
registry and the realignment machinery are shared by plans #904–#907, which only
add their keys to it; nothing may reintroduce a per-install switch for behaviour
these plans made unconditional.
