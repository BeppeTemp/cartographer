---
topic: project-governance
---

# D259 — Published text carries no private names

**Decision.** The repository is public, so nothing it publishes — commits, docs, tests, issues,
PRs, comments, release notes — carries a real name from a private setup: KB names, hosts,
usernames, employer, client or project names. Measurements and reproductions use placeholders
(`kb-a`, `kb-b`, `work-kb`, `gitlab.example.com`). The `privacy` workflow enforces it: it scans
pushed commits, PR diffs and issue/PR/comment text against terms that exist only as HMAC
digests in the `PRIVACY_GUARD_DIGESTS` secret (key `PRIVACY_GUARD_KEY`); a hit fails the run and
replaces the term in the text with `[redacted]`.

**Why.** Plan issues and decision records are written by agents from real sessions, and they
copied real KB names, a work Git remote and a username into the public history. Removing them
took a history rewrite, deleting issues and a GitHub Support request, and copies in forks and
the Go module proxy cannot be recalled. The rule has to hold before publication, so the same
check also runs on the maintainer's machines (a `gh` shim and a pre-push hook, outside this
repository); the workflow is the net for whatever gets past them.

**Alternatives rejected.**
- A plain term list in the repository or in the workflow: it would publish the very names it
  protects. Unkeyed hashes of short words are reversible by dictionary; the HMAC key is secret.
- Relying on the instruction alone: the leaks were written by agents that had no reason to
  think a KB name was private.

**Consequences.** A redaction edits the text, but GitHub keeps the original revision in the edit
history: the failed run is the signal to delete that revision by hand. Forks get no secrets, so
the workflow skips there. Adding a term means appending its digest to the secret and to the
maintainer's local digests file, never writing the term anywhere.
