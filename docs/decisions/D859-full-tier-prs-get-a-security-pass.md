---
topic: project-governance
---

# D859 — Full-tier PRs get a separate security-reviewer pass

**Decision.** On the full tier of the `review-pr` skill, a fourth repository
subagent, `security-reviewer`, runs in parallel with `pr-reviewer` on the same
read-only worktree. It reads with the attacker's view; the threat model lives in
`SECURITY.md` §Scope, not in the agent. A verified high or medium security
finding blocks the verdict and the merge, whatever standing approval exists. A
`pre-existing` security finding is never made public: it becomes a private
GitHub security advisory drafted for the user, never a `bug` issue or a comment.

**Why.** `pr-reviewer` looks for broken invariants and contracts, which is not
the same question as "how would I abuse this". The full tier is exactly the code
where a defect is a vulnerability (auth, SOPS, provisioning, service, gitx,
mcpserver, CI, installers, release, bundled skills). Public issues about
released code would publish the vulnerability before a fix.

**Alternatives rejected.**
- Claude Code's `/security-review`: one client only, and it does not know this
  project's threat model.
- A security pass on every tier: the cost is not worth it outside the full tier.
- Folding security into `pr-reviewer`: one prompt doing two jobs dilutes both.

**Consequences.** Full-tier review costs a second subagent run per round. The
definition exists in the three client formats and must stay identical (D327,
parity test). A change to the threat model goes to `SECURITY.md` §Scope, not to
the agent text.
