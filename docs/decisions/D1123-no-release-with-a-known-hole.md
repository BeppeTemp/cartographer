---
topic: deployment-release
---

# D1123 — The release train merges only when the security gate is clear

**Decision.** The Monday train (D956) merges the release PR only when there is no
open Dependabot alert, no open code scanning alert and no draft security advisory
whose fix PR is still open in its private fork. A fix for an advisory lands by
Friday, before the freeze, so the Saturday candidate carries it. The check reads with
`SECURITY_GATE_TOKEN`, a read-only PAT; a missing token or a failed call counts as not
clear, and the skip comment never says which check failed.

**Why.** A release that ships a hole already known and fixed, or already flagged by
a scanner, is a choice nobody would make on purpose; the human `rc-ok` alone does not
stop it. The cost is a train that can skip a week for an alert in a dev dependency or
a false positive, which must then be bumped or dismissed with its reason, and a fix
that is public on `main` for about three days before its release (its commits state
the new behaviour, not the hole).

**Alternatives rejected.**
- Merge advisory fixes on Monday morning: the candidate would no longer be the head
  of `main`, so every week with a fix becomes a manual release after a rebuilt
  candidate.
- Block on every draft advisory, also a report without a fix: one hard report would
  stop every release, including the other security fixes.
- Read with `github.token`: it cannot read Dependabot alerts, repository advisories
  or the advisory's private fork.
- Name the failed check in the skip comment: the repository is public, and "a draft
  advisory is open" announces an unpublished vulnerability.

**Consequences.** `internal/repodocs/release_train_test.go` pins the gate's token
and that the merge step waits for its `clear` output. The maintainer closes an
advisory's fork PR once its fix is on `main`, or the gate stays shut.
