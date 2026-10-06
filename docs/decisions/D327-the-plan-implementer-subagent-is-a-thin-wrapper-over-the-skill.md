---
topic: project-governance
---

# D327 — The plan-implementer subagent is a thin per-client wrapper over the implement-issue skill

**Decision.** The repository ships one coding subagent, `plan-implementer`, in
each client's own project format — `.claude/agents/plan-implementer.md`,
`.codex/agents/plan-implementer.toml`, `.kiro/agents/plan-implementer.json` —
with the same text. That text does not restate the procedure: it points at the
"Canonical mandate" in `.agents/skills/implement-issue/SKILL.md` §2 and fixes
only the defaults a coordinator would otherwise repeat in every mandate (stay in
the worktree, never merge, stop on a stale plan, a ≤10-line report).

**Why.** Landing a batch of plans spawns one subagent per plan; with the mandate
inline, every spawn carries the same long prompt and every report comes back in
a different shape. A named agent shrinks the mandate to "issue #N, worktree X"
and makes the report predictable. The cost is three files that must say the same
thing, because no client reads another's agent format and a symlink cannot
translate between Markdown, TOML and JSON.

**Alternatives rejected.**
- Duplicating the mandate in each agent file: three copies of a procedure drift;
  the skill is already the single copy every client reads (D203).
- A Cartographer-provisioned agent: provisioning serves KB artifacts to a user's
  machine, not a repository's own contributor tooling.
- No agent, inline mandates only: works on every client, but costs the prompt on
  every spawn and leaves the report format to chance.

**Model.** Each definition pins a cheaper model than a coordinator usually runs on (Claude `claude-sonnet-5-5`, Kiro `claude-sonnet-5.5`, Codex `terra-6`): a plan issue already carries the design, the implementer executes it, and the coordinator's diff read is the quality gate. A model rename is a one-line change in each of the three files.

**Consequences.** A change to the mandate goes in the skill only. A change to the
wrapper's defaults goes in all three files in the same commit. Antigravity has no
documented project-local agent cell, so it keeps running the mandate inline.
