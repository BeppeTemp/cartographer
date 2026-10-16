---
topic: project-governance
---

# D857 — Reviews converge, and supply-chain files always get a full review

**Decision.** Four rules extend D856's review procedure (`review-pr` skill,
`pr-reviewer` subagent, `triage.sh`, PR template):

- A PR gets **at most three review rounds**. From round 2 the reviewer reads only
  the commits added since the round before and never repeats a finding; a high or
  medium finding still open after round 3 goes back to the user, because the
  approach is the problem rather than the next fix.
- A defect in lines the diff does not touch is labelled **`pre-existing`**: it
  does not block, is not fixed in the PR, and becomes a `bug` issue. A finding
  names its **class** and lists the other instances in the diff, so one fix
  covers all of them.
- **Supply-chain files** — CI workflows, `install.sh`/`install.ps1`, the release
  pipeline (`.goreleaser.yaml`, `Dockerfile`, `release-please-config.json`) and
  the skills under `internal/skillbundle/bundled/` — make a PR full tier at any
  size. The user reads those hunks and decides that merge: a standing approval for
  self-merge does not cover it. The bundled skills count as production code, not
  docs, because the binary installs them into users' agents.
- The PR template has a **`Revert`** section: what a `git revert` would not undo
  once a release has shipped the change.

**Why.** The pattern comes from the code-review process Anthropic describes for
Claude Code: let machines do most of the reading, but bound it. An agent author
and an agent reviewer can keep each other busy indefinitely, and every finding
costs a commit and another round. Most of what a reviewer flags in a PR turns out
to be older code; fixing it there grows the PR past its plan and keeps the loop
going. Under the old tiers, a 20-line change to a CI workflow or to `install.sh`
was "light" and got no subagent, although that code holds the release
credentials or runs on users' machines. A revert is not an undo here: a release
writes into users' home directories, installs a service and owns a KB on-disk
format, and none of that comes back with `git revert`.

**Alternatives rejected.**

- Review until clean, without a cap: each round finds less and costs the same,
  and an agent answers a minor finding with a commit, where a person would skip it.
- A panel of parallel reviewers voting on every finding: it multiplies the
  reviewer's cost on every PR. The coordinator already verifies each cited line;
  a separate security pass, limited to the full tier, is planned on its own.
- `CODEOWNERS` with a required code-owner review on supply-chain paths: GitHub does
  not let a sole maintainer approve their own PR, so every one of their PRs that
  touches those paths would be blocked.
- A `Risk` section in the template: `Invariants touched` already says where the
  risk is. What it does not say is what outlives a revert.

**Consequences.** `SUPPLY_CHAIN` in `triage.sh` and the full-tier row of the
`review-pr` skill change together: a new installer, workflow-like file or shipped
prompt directory goes in both. The `pr-reviewer` mandate carries the round number,
and from round 2 also the commit already reviewed and the previous findings with
their answers. `triage.sh` warns when a PR body has no `## Revert` section.
