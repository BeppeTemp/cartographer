---
topic: project-governance
---

# D81 — Agent-neutral workflow: AGENTS.md canonical, plans as GitHub issues

**Status: implemented (2026-07-20).**

**Context.** After the open source release (D79) the repo's agent-facing surface was still Claude-only: instructions lived in `CLAUDE.md` (a filename only Claude Code reads) and the design→implementation handoff in a Claude skill that committed plans to `docs/plans/` on `main`. That flow was broken by D79.d (protected `main`: landing a plan now takes one PR, deleting it another) and produced `docs(plans):` noise in the public history; a Claude-only repo was also inconsistent with Cartographer's own multi-provider configurator.

**Decision.**

a) **`AGENTS.md` is the canonical agent-instructions file** (the cross-agent convention read natively by Codex, OpenCode, Kiro and others); `CLAUDE.md` remains as a symlink to it, so Claude Code keeps working unchanged.

b) **Plans move from `docs/plans/` files to GitHub issues** (label `plan`, template `.github/ISSUE_TEMPLATE/plan.md`). The agent-neutral procedure — self-sufficiency test included — lives in `CONTRIBUTING.md` §Plan issues; `.claude/skills/plan-issue` shrinks to Claude-side glue (D-number reservation, graphify pointers, `gh issue create`/`view`). The implementation PR closes the issue (`Closes #<n>`); a consumed plan survives as a closed issue instead of a deleted file.

**Rationale.** Issues give the handoff artifact first-class linkage to the PR, an archive that survives consumption, zero service commits in the public history, and readability from any agent with `gh` — the file-based flow had none of these once `main` became protected.
