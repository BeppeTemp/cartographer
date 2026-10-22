---
topic: project-governance
---

# D1020 — Security scans run in their own workflow and are not required checks

**Decision.** `security.yml` runs govulncheck, `npm audit` on the production
dependencies, actionlint, zizmor and CodeQL on push to main, on PRs that touch
code, dependencies or workflows, and weekly. None of it is a required check.
Every `uses:` in `.github/workflows/` is pinned by full commit SHA with a
`# vX.Y.Z` comment, and `internal/repodocs/workflows_test.go` fails on any that
is not.

**Why.** A new advisory against an old dependency appears without any change of
ours; as a required check it would block unrelated PRs, so a failed scan is
reported instead (a `vuln-scan` issue with the run link, the Security tab). The
scans live apart from `ci.yml` so their schedule, permissions
(`security-events: write` only on the CodeQL job) and failure policy stay
separate from the gate. A tag is a pointer its owner can move, and these
workflows hold a release token and publish artifacts: a SHA cannot move, and a
test is the only thing that keeps the next edit from putting a tag back. The
cost is that Dependabot, not a reader, now tracks the versions, and a bump PR
shows a hash.

**Alternatives rejected.**
- Required scan checks: blocks every PR on advisories it did not cause.
- Scans inside `ci.yml`: couples a scheduled, advisory job to the gate's triggers and permissions.
- GitHub's CodeQL default setup: cannot be reviewed or versioned in the repository; it must stay disabled so the two do not upload the same analysis twice.
- zizmor alone for pinning: it is advisory here and can be skipped; the test runs in `make gate`.
- Publishing the finding in the `vuln-scan` issue: a reachable vulnerability in our own code goes through a private advisory; the issue carries only the run link.

**Consequences.** A new action is added pinned, with its version in the comment.
Fork PRs skip the CodeQL job (read-only token); main and the weekly run still
analyse the merged code. Repository settings that no file can enforce (a tag
ruleset for `v*`, "require actions pinned to a full SHA", approval for fork-PR
workflows) are listed in `docs/deployment.md` for the maintainer. Docker
Dependabot PRs are not auto-merged, because the `docker` job is not a required
check.
