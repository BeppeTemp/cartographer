---
topic: deployment-release
---

# D39 — Server-side KB bootstrap from git remote (replaces the k8s init container)

> **Amended by [D905](D905-one-credentials-directory-audit-always-on.md)**: the bootstrap SSH key and `known_hosts` are `<kb>.ssh` (else `default.ssh`) and `known_hosts` in the credentials directory; `git.ssh_key` and `git.known_hosts` are removed, and an explicit `GIT_SSH_COMMAND` still wins.

**Decision.** `cartographer serve` clones each `kbs: [{remote: ...}]`/`CARTOGRAPHER_KB_REMOTES` into
`<data>/<nome>` before opening the KBs, only if the destination does not already exist; `GIT_SSH_COMMAND` is
built from `git.ssh_key`/`git.known_hosts` if not already present in the environment (env always wins).
**Rationale.** The previous k8s deployment required a separate init container for the initial
clone — one more failure point, no sharing with the existing fetch/pull-rebase path.
Bringing it into the `serve` process removes the init container; "skip if already present" avoids
re-clones at every pod restart.
Details: `docs/deployment.md` §KB bootstrap from git remote.
