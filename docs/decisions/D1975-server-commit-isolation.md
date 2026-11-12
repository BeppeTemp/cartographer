---
topic: concurrency-git
---

# D1975 — Server commits ignore the user's commit-time git config, and a refusal no retry changes fails at once

**Decision.** Every git command of the server runs with `commit.gpgsign=false` and an empty `core.hooksPath`
(`serverConfig`), and `git commit` is bounded by `gitx.CommitTimeout`. A push the remote refuses for a reason a retry
does not change (a credential without write access, a repository rule, a required signature, a file over the size
limit, a server-side hook that declined it) is not retried: the sync state is `failed` after one attempt with a
one-line remedy quoting the remote's own line. Only that policy wording is permanent: a bare HTTP 403 (it can be a rate
limit) and a `[remote rejected]` for a lock, a ref update, an internal or unpacker error are races or server faults and
stay retried. `setup` probes push access
with `git push --dry-run` before it changes anything.

**Why.** The server is a background service run with the user's global git config. `commit.gpgsign=true` made a write
wait for a pinentry prompt nobody could answer, and the user's `core.hooksPath` failed every write on a hook written for
that user's own commits; neither is a decision of the KB. And a refusal that is the remote's rule was retried five
times and ended as `push failed after 5 attempts`, hiding the one line that said what to change. The test KBs already
isolated themselves this way, production did not.

**Alternatives rejected.**
- Sign the server's commits: it needs a key and an agent the service does not have; not supported, and the docs say so.
- Honor the user's hooks and signing, with a timeout only: every write would still fail or stall, just later.
- Probe by pushing a real branch: it writes to the remote; a dry run catches a read-only credential without writing.
- Keep treating unknown refusals as transient: a rule or a missing right never heals by itself, and five attempts hold
  the KB's git lock for nothing.

**Consequences.** The override is command-line configuration, so it wins over a KB's own `.git/config` too: the server
owns its clones, and per-KB hooks and signing are not supported on server commits. Hooks of the user's own commits
(`pre-commit`, `commit-msg`) never run on a server commit, and a
signature requirement on the remote makes every push fail until the rule is lifted for the branch. The dry-run probe
sees access only: rules the remote evaluates on a real push show at the first push. A new refusal wording is one row
of `permanentPushRules`; the protected-branch and identity tests stay first, because their hook wording is also a
`[remote rejected]`.
