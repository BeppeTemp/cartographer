---
topic: concurrency-git
---

# D1004 — Git never prompts, and a push is bounded

**Decision.** `gitEnv`, the environment of every git command gitx runs, sets
`GIT_TERMINAL_PROMPT=0` (before the caller's own variables, which can still
override it). Commands that talk to a remote (`Fetch`, `Push`, `PushSetUpstream`)
also run ssh in batch mode (`GIT_SSH_COMMAND`), unless the caller or process sets
`GIT_SSH_COMMAND` or `GIT_SSH`, or the repository sets `core.sshCommand`. The
command `sshCommand` builds for a provisioned key carries `BatchMode=yes` itself.
`SyncOut` does not retry a timed-out push. `Push` and
`PushSetUpstream` are bounded by `PushTimeout` (60s, a package variable like
`FetchTimeout`), are killed on expiry (the process group on Unix; on Windows git alone, `WaitDelay` bounding the pipes) and report a
`RemoteTimeoutError`; a failed push carries the same remedy line as a clone.

**Why.** Only `Clone` was non-interactive. A server started from a terminal that
met a 401 on push blocked on `/dev/tty` while holding the KB's git lock, so the
write path hung until someone typed. Push was also unbounded. 60s rather than 15s
because a push carries data; like `FetchTimeout` it is not a setting.

**Alternatives rejected.** Forcing `GIT_SSH_COMMAND` unconditionally: it overrides
`core.sshCommand`, replacing an operator's proxy command or identity file.
