---
topic: concurrency-git
---

# D117 — Server Git profile uses a dedicated working branch and GitHub PR boundary

**Decision.** Keep Local Core as the default `git.profile: local`. The opt-in `server`
profile requires an explicit protected base, a dedicated working branch, GitHub
repository/API coordinates and a token environment variable. It mounts only a clean,
attached checkout, fetches `origin`, resumes a verified working branch or creates it
from the remote base, and never repairs ambiguity by discarding files or commits.

Writes rebase the working branch onto the base and push only that branch. The server
uses an injected stdlib-HTTP GitHub forge boundary to maintain exactly one open PR,
persisting only non-secret identity/state under `.cartographer/`. Finalization requires
a current caller head plus review/check readiness, rebases and validates, uses
force-with-lease solely for the working branch, then requests a squash merge. Local
reconciliation failures remain degraded and recoverable; the base branch is never a Git
push target.

**Rationale.** A long-lived server needs reviewability without treating a protected
branch as its synchronization transport. Separating Git transport from the forge API
keeps the contract testable with local fixtures today and leaves a narrow extension
point for another forge later.
