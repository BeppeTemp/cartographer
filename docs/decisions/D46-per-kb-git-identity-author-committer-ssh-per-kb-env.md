---
topic: concurrency-git
---

# D46 — Per-KB git identity (author/committer + SSH): per-KB env wins over the process, default committer = author

> **Amended by [D904](D904-the-writer-is-the-author.md)**: the per-KB author and committer are removed (the writer is the author, the server the committer); the per-KB SSH key and credentials still win over the process.

> **Amended by [D905](D905-one-credentials-directory-audit-always-on.md)**: the per-KB SSH key is `<kb>.ssh` in the credentials directory (`kbs[].ssh_key` and `kbs[].known_hosts` are removed); it still wins over the process.

**Decision.** Connects the identity fields of `KBSpec`/`GitConfig` (D44) to the actual git commands.
`internal/gitx.runGitEnv(dir, env, args...)` runs git with `env` **winning** over the process on
duplicate keys; `Clone`/`Commit`/`Fetch`/`Push`/`PullRebaseAutostash` take a variadic
`env ...string`. `kb.KB` gains `GitAuthorName`/`GitAuthorEmail`/`GitEnv`, used by `CommitOp`/
`SyncIn`/`SyncOut`/conflict resolution. `gitEnvForKB` assembles `GIT_SSH_COMMAND` and
`GIT_COMMITTER_*` with cascading fallback: spec → global → default (default committer = author).
**Rationale.** The "per-KB env wins over the process" inversion is deliberately opposite to
`setupGitSSH`'s "the environment wins" rule (global SSH fallback): at the per-KB level the identity is an
explicit config choice and must prevail over an inherited process environment that could
carry the wrong identity. The cascading fallback (KB → global → hardcoded) keeps zero-value
= pre-existing behavior on unchanged deployments.
Details: `docs/transport-auth.md`, `docs/concurrency.md` §Git synchronization to the remote.
