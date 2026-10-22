---
topic: project-governance
---

# D884 — Cartographer is developed with Claude Code, Kiro, OpenCode and Copilot CLI, on Claude models, with five subagents

> **Superseded by [D992](D992-claude-code-only-dev-tooling.md)**: the repository is developed with Claude Code only; the Kiro, OpenCode and Copilot CLI surfaces and the `.agents/skills/` tree are removed.

**Decision.** The clients this repository is equipped to be developed with are
Claude Code, Kiro, OpenCode and Copilot CLI, and any of the four runs the whole
workflow (`plan-issue`, `implement-issue`, `live-dev`, `review-pr`,
`harness-watch`) with the same subagents. Codex and Antigravity leave the
development tooling: their agent files, the Codex UI metadata of the skills and
the Codex-only gates are deleted. Product support for them (the configurator,
the provisioning matrix, the `docs/harnesses.md` ledger) is a different thing and
does not change.

Every subagent runs on a Claude model, written the way each client spells it:
Claude Code `claude-sonnet-5-5`, Kiro and Copilot CLI `claude-sonnet-5.5`,
OpenCode `anthropic/claude-sonnet-5-5`; the new read-only `reader` runs on Haiku
5.5 (`claude-haiku-5-5`, `claude-haiku-5.5`, `anthropic/claude-haiku-5-5`).
`reader` reads one voluminous source and returns a digest: the failed log of a
red CI run, or one client's release notes for `harness-watch`. The reviewers stay
on Sonnet.

Each agent exists in four files: `.claude/agents/<n>.md`, `.kiro/agents/<n>.json`,
`.opencode/agents/<n>.md` (`mode: subagent`; `reader` also `permission.edit:
deny`) and `.github/agents/<n>.agent.md`. `TestRepoSubagentsSayTheSameThingOnEveryClient`
checks that the body, the description and the model translated by that rule
agree with the Claude file, and refuses a client file with no Claude
counterpart. The skills stay once in `.agents/skills/`, with a symlink bridge
`.opencode/skills/` beside `.claude/skills/` and `.kiro/skills/`; Copilot CLI
reads `.agents/skills/` natively.

This partly supersedes D327 (the model pins: Codex's `terra-6` goes, the rest
becomes the rule above), D203 (Codex as the reason `.agents/skills` is the
canonical directory: Copilot CLI reads it natively, and nothing else about the
layout changes) and D364 (`.agents/agents` as a development cell: the workspace
layer of Antigravity is no longer one we develop with).

**Why.** Nobody develops this repository with Codex or Antigravity, and keeping
their files costs a parity check, a gate (the 32 KiB `AGENTS.md` chain was
Codex's `project_doc_max_bytes`) and a table row each. OpenCode and Copilot CLI
are used and had no subagents: the coordinator on those clients ran the whole
workflow inline, which is what the subagents exist to avoid. Pinning every
client to a Claude model keeps the implementer, the reviewers and the reader
comparable across clients, so a finding or a report means the same thing
whichever client produced it. Reading a CI log or a release note is the
expensive, judgement-free part of two procedures; on Haiku the coordinator pays
for a digest instead of a log.

**Alternatives rejected.**
- Keeping Codex and Antigravity as development clients: unused, and each costs
  a file per agent plus gates that only they need.
- Two readers, one for CI and one for release notes: both jobs are "read a lot,
  return a little", and every agent costs one file on each client.
- The reviewers on Haiku as well: a weaker reviewer misses defects that then
  cost a stronger model's time.
- No model pin on OpenCode and Copilot CLI: they would run on whatever the
  session picked, and the parity of the four clients would stop at the body.

**Consequences.**
- OpenCode does not load `.claude/agents/`: its project agents are
  `.opencode/agents/` (`opencode debug agents` lists them). Copilot CLI loads
  `.github/agents/` only once the folder is trusted, and honours the `model`
  key: a model the subscription does not offer falls back to `auto` with a
  warning. Kiro's model list depends on the subscription in the same way. The
  repository keeps the pins; a developer's subscription is not its concern.
- The spawn tool differs per client and lives in the per-client tables of the
  skills: OpenCode 2.0.30 delegates with its `subagent` tool (`agent: <name>`),
  Copilot CLI with `task` (`agent_type: <name>`).
- A new agent is four files, one per client; the parity test fails until they
  agree.
